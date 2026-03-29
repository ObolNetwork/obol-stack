"""Serve manager — manages llama-server systemd service for model serving."""

import logging
import subprocess
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Dict, Optional

from .registry import ModelStatus, Registry
from .hardware import compute_optimal_ngl, get_hardware_profile

logger = logging.getLogger(__name__)

LLAMA_SERVER_BINARY = Path.home() / "Development" / "llama-cpp-turboquant-cuda" / "build" / "bin" / "llama-server"
SERVICE_NAME = "obol-inference"
DEFAULT_PORT = 8080
DEFAULT_CONTEXT = 8192


@dataclass
class ServingStatus:
    """Current serving status."""
    active: bool
    model_id: Optional[str]
    model_name: Optional[str]
    port: int
    healthy: bool
    uptime_seconds: Optional[float]
    pid: Optional[int]


def generate_systemd_unit(model_path: str, ngl: int, port: int = DEFAULT_PORT,
                          threads: int = 8, context_size: int = DEFAULT_CONTEXT) -> str:
    """Generate a systemd unit file for llama-server.

    Args:
        model_path: Absolute path to the GGUF model file.
        ngl: Number of GPU layers to offload.
        port: Port to listen on.
        threads: Number of CPU threads.
        context_size: Context window size.

    Returns:
        Complete systemd unit file content as string.
    """
    unit = f"""[Unit]
Description=Obol Inference Server (llama-server)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User={_get_current_user()}
Environment=CUDA_VISIBLE_DEVICES=0
ExecStart={LLAMA_SERVER_BINARY} \\
    -m {model_path} \\
    -ngl {ngl} \\
    --port {port} \\
    -t {threads} \\
    -c {context_size} \\
    --host 0.0.0.0 \\
    --metrics \\
    --log-disable
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
"""
    return unit


def _get_current_user() -> str:
    """Get the current username."""
    import os
    return os.environ.get("USER", "obol")


def install_service(unit_content: str, service_name: str = SERVICE_NAME) -> None:
    """Install a systemd service unit file.

    Writes the unit file to /etc/systemd/system/, runs daemon-reload,
    and enables the service.

    Args:
        unit_content: Complete systemd unit file content.
        service_name: Name of the systemd service.

    Raises:
        RuntimeError: If installation fails.
    """
    unit_path = f"/etc/systemd/system/{service_name}.service"
    logger.info("Installing systemd service: %s", unit_path)

    # Write unit file (requires sudo)
    proc = subprocess.run(
        ["sudo", "tee", unit_path],
        input=unit_content, capture_output=True, text=True
    )
    if proc.returncode != 0:
        raise RuntimeError(f"Failed to write unit file: {proc.stderr}")

    # Reload systemd
    proc = subprocess.run(["sudo", "systemctl", "daemon-reload"],
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"daemon-reload failed: {proc.stderr}")

    # Enable service
    proc = subprocess.run(["sudo", "systemctl", "enable", service_name],
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"Failed to enable service: {proc.stderr}")

    logger.info("Service %s installed and enabled", service_name)


def _health_check(port: int = DEFAULT_PORT, timeout: int = 30) -> bool:
    """Check if llama-server is healthy.

    Args:
        port: Port to check.
        timeout: Maximum seconds to wait for health.

    Returns:
        True if server responds healthy within timeout.
    """
    for _ in range(timeout):
        try:
            result = subprocess.run(
                ["curl", "-sf", f"http://localhost:{port}/health"],
                capture_output=True, text=True, timeout=5
            )
            if result.returncode == 0:
                return True
        except subprocess.TimeoutExpired:
            pass
        time.sleep(1)
    return False


def start_serving(model_id: str, registry: Registry,
                  port: int = DEFAULT_PORT) -> ServingStatus:
    """Start serving a model via systemd.

    Stops any currently serving model, installs the new service,
    starts it, verifies health, and updates the registry.

    Args:
        model_id: ID of the model to serve.
        registry: Model registry instance.
        port: Port to serve on.

    Returns:
        ServingStatus of the newly started service.

    Raises:
        RuntimeError: If serving fails to start.
    """
    model = registry.get_model(model_id)
    if model is None:
        raise ValueError(f"Model {model_id} not found")
    if not model.gguf_path:
        raise ValueError(f"Model {model_id} has no GGUF path")

    # Stop current serving model
    stop_serving(registry)

    # Generate and install service
    hw = get_hardware_profile()
    ngl = compute_optimal_ngl(model.size_gb or 4.0, hw.vram_gb)
    unit = generate_systemd_unit(
        model_path=model.gguf_path, ngl=ngl,
        port=port, threads=hw.cpu_cores, context_size=DEFAULT_CONTEXT
    )
    install_service(unit)

    # Start service
    proc = subprocess.run(["sudo", "systemctl", "start", SERVICE_NAME],
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"Failed to start service: {proc.stderr}")

    # Health check
    if not _health_check(port):
        subprocess.run(["sudo", "systemctl", "stop", SERVICE_NAME],
                       capture_output=True, text=True)
        raise RuntimeError("Service started but failed health check")

    # Update registry
    registry.promote(model_id)
    logger.info("Model %s (%s) now serving on port %d", model_id, model.name, port)

    return get_serving_status(registry)


def stop_serving(registry: Registry) -> None:
    """Stop the current serving model.

    Args:
        registry: Model registry instance.
    """
    current = registry.get_serving_model()
    proc = subprocess.run(
        ["sudo", "systemctl", "stop", SERVICE_NAME],
        capture_output=True, text=True
    )
    if current:
        try:
            registry.retire(current.id)
        except ValueError:
            pass  # Already retired
    logger.info("Stopped inference service")


def hot_swap(new_model_id: str, registry: Registry,
             rollback_on_failure: bool = True,
             port: int = DEFAULT_PORT) -> ServingStatus:
    """Hot-swap to a new model with rollback support.

    Stops the current model, starts the new one, and rolls back
    if the new model fails health check.

    Args:
        new_model_id: ID of the model to swap to.
        registry: Model registry instance.
        rollback_on_failure: Whether to rollback on failure.
        port: Port to serve on.

    Returns:
        ServingStatus after swap.

    Raises:
        RuntimeError: If swap and rollback both fail.
    """
    old_model = registry.get_serving_model()
    old_model_id = old_model.id if old_model else None

    try:
        return start_serving(new_model_id, registry, port)
    except RuntimeError as e:
        logger.error("Hot swap to %s failed: %s", new_model_id, e)
        if rollback_on_failure and old_model_id:
            logger.info("Rolling back to %s", old_model_id)
            try:
                return start_serving(old_model_id, registry, port)
            except RuntimeError as rollback_err:
                raise RuntimeError(
                    f"Hot swap failed AND rollback failed: {e} / {rollback_err}"
                )
        raise


def get_serving_status(registry: Optional[Registry] = None) -> ServingStatus:
    """Get current serving status from systemd.

    Args:
        registry: Optional registry to look up model details.

    Returns:
        ServingStatus with current state.
    """
    # Check systemd service status
    proc = subprocess.run(
        ["systemctl", "is-active", SERVICE_NAME],
        capture_output=True, text=True
    )
    active = proc.stdout.strip() == "active"

    # Get PID
    pid = None
    if active:
        pid_proc = subprocess.run(
            ["systemctl", "show", SERVICE_NAME, "--property=MainPID", "--value"],
            capture_output=True, text=True
        )
        try:
            pid = int(pid_proc.stdout.strip())
        except ValueError:
            pass

    # Get model info from registry
    model_id = None
    model_name = None
    if registry:
        serving = registry.get_serving_model()
        if serving:
            model_id = serving.id
            model_name = serving.name

    # Health check (non-blocking)
    healthy = False
    if active:
        try:
            h = subprocess.run(
                ["curl", "-sf", f"http://localhost:{DEFAULT_PORT}/health"],
                capture_output=True, text=True, timeout=3
            )
            healthy = h.returncode == 0
        except subprocess.TimeoutExpired:
            pass

    return ServingStatus(
        active=active,
        model_id=model_id,
        model_name=model_name,
        port=DEFAULT_PORT,
        healthy=healthy,
        uptime_seconds=None,
        pid=pid,
    )
