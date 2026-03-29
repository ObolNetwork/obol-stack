"""Hardware profiler for inference lifecycle — detects GPU, RAM, disk, and computes optimal settings."""

import logging
import os
import platform
import re
import shutil
import subprocess
from dataclasses import dataclass
from typing import Optional, Tuple

logger = logging.getLogger(__name__)


@dataclass
class HardwareProfile:
    """Complete hardware profile for inference planning."""
    gpu_name: str
    gpu_backend: str  # cuda, rocm, metal, cpu
    vram_gb: float
    ram_gb: float
    disk_free_gb: float
    cpu_cores: int
    os_name: str
    arch: str


def detect_gpu() -> Tuple[str, str, float]:
    """Detect GPU name, backend, and VRAM in GB.

    Tries nvidia-smi first, then rocm-smi, then falls back to CPU.
    Returns (gpu_name, backend, vram_gb).
    """
    # Try NVIDIA
    if shutil.which("nvidia-smi"):
        try:
            result = subprocess.run(
                ["nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                line = result.stdout.strip().split("\n")[0]
                parts = [p.strip() for p in line.split(",")]
                gpu_name = parts[0]
                vram_mb = float(parts[1])
                vram_gb = round(vram_mb / 1024, 1)
                logger.info("Detected NVIDIA GPU: %s with %.1f GB VRAM", gpu_name, vram_gb)
                return gpu_name, "cuda", vram_gb
        except (subprocess.TimeoutExpired, IndexError, ValueError) as e:
            logger.warning("nvidia-smi failed: %s", e)

    # Try ROCm
    if shutil.which("rocm-smi"):
        try:
            result = subprocess.run(
                ["rocm-smi", "--showmeminfo", "vram", "--csv"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                # Parse ROCm CSV output for VRAM total
                lines = result.stdout.strip().split("\n")
                for line in lines[1:]:  # skip header
                    if "total" in line.lower() or len(lines) == 2:
                        nums = re.findall(r"[\d.]+", line)
                        if nums:
                            vram_bytes = float(nums[-1])
                            vram_gb = round(vram_bytes / (1024**3), 1)
                            break
                else:
                    vram_gb = 0.0
                # Get GPU name
                name_result = subprocess.run(
                    ["rocm-smi", "--showproductname"],
                    capture_output=True, text=True, timeout=10
                )
                gpu_name = "AMD GPU"
                if name_result.returncode == 0:
                    for l in name_result.stdout.split("\n"):
                        if "card" in l.lower() or "gpu" in l.lower():
                            gpu_name = l.strip().split(":")[-1].strip() or gpu_name
                            break
                logger.info("Detected ROCm GPU: %s with %.1f GB VRAM", gpu_name, vram_gb)
                return gpu_name, "rocm", vram_gb
        except (subprocess.TimeoutExpired, ValueError) as e:
            logger.warning("rocm-smi failed: %s", e)

    # macOS Metal
    if platform.system() == "Darwin":
        try:
            result = subprocess.run(
                ["system_profiler", "SPDisplaysDataType"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                vram_match = re.search(r"VRAM.*?(\d+)\s*(MB|GB)", result.stdout, re.IGNORECASE)
                name_match = re.search(r"Chipset Model:\s*(.+)", result.stdout)
                gpu_name = name_match.group(1).strip() if name_match else "Apple GPU"
                vram_gb = 0.0
                if vram_match:
                    val = float(vram_match.group(1))
                    vram_gb = val if vram_match.group(2).upper() == "GB" else val / 1024
                logger.info("Detected Metal GPU: %s with %.1f GB VRAM", gpu_name, vram_gb)
                return gpu_name, "metal", vram_gb
        except (subprocess.TimeoutExpired, ValueError) as e:
            logger.warning("system_profiler failed: %s", e)

    logger.info("No GPU detected, falling back to CPU")
    return "CPU", "cpu", 0.0


def detect_system() -> Tuple[float, float, int]:
    """Detect system RAM (GB), disk free (GB), and CPU cores.

    Returns (ram_gb, disk_free_gb, cpu_cores).
    """
    # RAM
    ram_gb = 0.0
    try:
        result = subprocess.run(["free", "-b"], capture_output=True, text=True, timeout=5)
        if result.returncode == 0:
            for line in result.stdout.split("\n"):
                if line.startswith("Mem:"):
                    parts = line.split()
                    ram_gb = round(float(parts[1]) / (1024**3), 1)
                    break
    except (subprocess.TimeoutExpired, FileNotFoundError):
        # Fallback for non-Linux
        try:
            import psutil
            ram_gb = round(psutil.virtual_memory().total / (1024**3), 1)
        except ImportError:
            ram_gb = 0.0

    # Disk free
    disk_free_gb = 0.0
    try:
        stat = os.statvfs(os.path.expanduser("~"))
        disk_free_gb = round(stat.f_bavail * stat.f_frsize / (1024**3), 1)
    except OSError:
        pass

    # CPU cores
    cpu_cores = os.cpu_count() or 1

    logger.info("System: %.1f GB RAM, %.1f GB disk free, %d CPU cores",
                ram_gb, disk_free_gb, cpu_cores)
    return ram_gb, disk_free_gb, cpu_cores


def compute_optimal_ngl(model_size_gb: float, vram_gb: float,
                        total_layers: int = 80) -> int:
    """Estimate optimal number of GPU layers to offload.

    Heuristic: (vram_gb - 2.0) / model_size_gb * total_layers, capped at 999.
    Reserves 2 GB VRAM for KV cache and OS overhead.

    Args:
        model_size_gb: Model file size in GB (rough proxy for weight memory).
        vram_gb: Available VRAM in GB.
        total_layers: Estimated total layers in the model.

    Returns:
        Number of layers to offload to GPU (0 if insufficient VRAM).
    """
    if vram_gb <= 2.0 or model_size_gb <= 0:
        return 0
    usable_vram = vram_gb - 2.0
    ngl = int((usable_vram / model_size_gb) * total_layers)
    ngl = max(0, min(ngl, 999))
    logger.debug("compute_optimal_ngl: %.1f GB model, %.1f GB VRAM -> ngl=%d",
                 model_size_gb, vram_gb, ngl)
    return ngl


def get_hardware_profile() -> HardwareProfile:
    """Build and return a complete hardware profile."""
    gpu_name, gpu_backend, vram_gb = detect_gpu()
    ram_gb, disk_free_gb, cpu_cores = detect_system()
    return HardwareProfile(
        gpu_name=gpu_name,
        gpu_backend=gpu_backend,
        vram_gb=vram_gb,
        ram_gb=ram_gb,
        disk_free_gb=disk_free_gb,
        cpu_cores=cpu_cores,
        os_name=platform.system(),
        arch=platform.machine(),
    )
