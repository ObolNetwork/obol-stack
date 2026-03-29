"""FastAPI routes for inference lifecycle management (Issue #300)."""

import logging
import uuid
from dataclasses import asdict
from typing import Dict, List, Optional

from fastapi import FastAPI, HTTPException, BackgroundTasks
from pydantic import BaseModel

from .registry import Registry, ModelStatus
from .hardware import get_hardware_profile
from .discover import search_localllama, parse_candidates, filter_candidates
from .validate import validate_model, ValidationResult
from .serve import hot_swap, start_serving, stop_serving, get_serving_status

logger = logging.getLogger(__name__)

app = FastAPI(
    title="Obol Inference Lifecycle API",
    description="Model discovery, validation, and serving management",
    version="0.1.0",
)

# Module-level registry instance (initialized on startup)
_registry: Optional[Registry] = None
_validation_jobs: Dict[str, dict] = {}


def get_registry() -> Registry:
    """Get or create the global registry instance."""
    global _registry
    if _registry is None:
        _registry = Registry()
    return _registry


# --- Request/Response Models ---

class DiscoverRequest(BaseModel):
    max_results: int = 20
    max_size_gb: Optional[float] = None


class ValidateRequest(BaseModel):
    url: str
    eval_suite: str = "toolcall-15"
    quant: Optional[str] = None
    name: Optional[str] = None


class ServeRequest(BaseModel):
    model_id: str
    rollback_on_failure: bool = True
    port: int = 8080


class ModelResponse(BaseModel):
    id: str
    name: str
    status: str
    quant: Optional[str] = None
    size_gb: Optional[float] = None
    toolcall15_score: Optional[float] = None
    tok_s_gen: Optional[float] = None
    tok_s_prompt: Optional[float] = None
    source_url: Optional[str] = None
    signal_score: Optional[float] = None


# --- Lifecycle Events ---

@app.on_event("startup")
async def startup():
    """Initialize registry on startup."""
    global _registry
    _registry = Registry()
    logger.info("Inference lifecycle API started")


@app.on_event("shutdown")
async def shutdown():
    """Clean up on shutdown."""
    if _registry:
        _registry.close()


# --- API Endpoints ---

@app.post("/api/v1/inference/discover")
async def discover_models(request: DiscoverRequest) -> Dict:
    """Trigger model discovery via social signal scraping.

    Searches x/LocalLLaMA for new model announcements and GGUF releases.
    Returns ranked candidates by signal score.
    """
    try:
        raw_json = search_localllama(max_results=request.max_results)
        candidates = parse_candidates(raw_json)

        if request.max_size_gb:
            candidates = filter_candidates(candidates, request.max_size_gb)

        # Register discovered candidates
        registry = get_registry()
        registered = []
        for c in candidates:
            record = registry.add_model(
                name=c.name, source_url=c.hf_url,
                quant=c.quant, size_gb=c.size_gb_est,
                signal_score=c.signal_score,
            )
            registered.append({"id": record.id, "name": c.name,
                              "hf_url": c.hf_url, "quant": c.quant,
                              "size_gb_est": c.size_gb_est,
                              "signal_score": c.signal_score})

        return {"candidates": registered, "total": len(registered)}
    except Exception as e:
        logger.error("Discovery failed: %s", e)
        raise HTTPException(status_code=500, detail=str(e))


def _run_validation_job(job_id: str, url: str, name: Optional[str],
                        quant: Optional[str]) -> None:
    """Background validation job runner."""
    from .discover import DiscoveryCandidate
    from datetime import datetime

    _validation_jobs[job_id]["status"] = "running"
    try:
        candidate = DiscoveryCandidate(
            name=name or url.split("/")[-1],
            hf_url=url, quant=quant, size_gb_est=None,
            signal_score=0.0, source_tweet_id="manual",
            discovered_at=datetime.utcnow().isoformat(),
        )
        hw = get_hardware_profile()
        registry = get_registry()
        result = validate_model(candidate, hw, registry)
        _validation_jobs[job_id]["status"] = "completed"
        _validation_jobs[job_id]["result"] = {
            "model_id": result.model_id,
            "passed": result.passed,
            "toolcall15_score": result.toolcall15_score,
            "tok_s_gen": result.tok_s_gen,
            "tok_s_prompt": result.tok_s_prompt,
            "error": result.error,
        }
    except Exception as e:
        _validation_jobs[job_id]["status"] = "failed"
        _validation_jobs[job_id]["error"] = str(e)
        logger.error("Validation job %s failed: %s", job_id, e)


@app.post("/api/v1/inference/validate")
async def validate_model_endpoint(request: ValidateRequest,
                                  background_tasks: BackgroundTasks) -> Dict:
    """Start model validation as a background job.

    Downloads the model, runs benchmarks, and evaluates tool-calling ability.
    Returns a job_id to track progress.
    """
    job_id = str(uuid.uuid4())[:12]
    _validation_jobs[job_id] = {"status": "queued", "url": request.url}
    background_tasks.add_task(
        _run_validation_job, job_id, request.url, request.name, request.quant
    )
    return {"job_id": job_id, "status": "queued"}


@app.get("/api/v1/inference/validate/{job_id}")
async def get_validation_status(job_id: str) -> Dict:
    """Check status of a validation job."""
    if job_id not in _validation_jobs:
        raise HTTPException(status_code=404, detail=f"Job {job_id} not found")
    return _validation_jobs[job_id]


@app.get("/api/v1/inference/models")
async def list_models(status: Optional[str] = None) -> Dict:
    """List all models in the registry, optionally filtered by status."""
    registry = get_registry()
    status_filter = ModelStatus(status) if status else None
    models = registry.list_models(status_filter)
    return {
        "models": [
            ModelResponse(
                id=m.id, name=m.name, status=m.status.value,
                quant=m.quant, size_gb=m.size_gb,
                toolcall15_score=m.toolcall15_score,
                tok_s_gen=m.tok_s_gen, tok_s_prompt=m.tok_s_prompt,
                source_url=m.source_url, signal_score=m.signal_score,
            ).dict()
            for m in models
        ],
        "total": len(models),
    }


@app.put("/api/v1/inference/models/{model_id}/promote")
async def promote_model(model_id: str) -> Dict:
    """Promote a model to serving status."""
    registry = get_registry()
    try:
        model = registry.promote(model_id)
        return {"id": model.id, "name": model.name, "status": model.status.value}
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))


@app.put("/api/v1/inference/models/{model_id}/retire")
async def retire_model(model_id: str) -> Dict:
    """Retire a model from serving."""
    registry = get_registry()
    try:
        model = registry.retire(model_id)
        return {"id": model.id, "name": model.name, "status": model.status.value}
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))


@app.get("/api/v1/inference/status")
async def inference_status() -> Dict:
    """Get current serving model and health status."""
    registry = get_registry()
    status = get_serving_status(registry)
    return {
        "active": status.active,
        "healthy": status.healthy,
        "model_id": status.model_id,
        "model_name": status.model_name,
        "port": status.port,
        "pid": status.pid,
    }


@app.post("/api/v1/inference/serve")
async def serve_model(request: ServeRequest) -> Dict:
    """Hot-swap to serve a specific model."""
    registry = get_registry()
    try:
        status = hot_swap(
            request.model_id, registry,
            rollback_on_failure=request.rollback_on_failure,
            port=request.port,
        )
        return {
            "active": status.active,
            "healthy": status.healthy,
            "model_id": status.model_id,
            "model_name": status.model_name,
            "port": status.port,
        }
    except (ValueError, RuntimeError) as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/api/v1/inference/hardware")
async def hardware_profile() -> Dict:
    """Get current hardware profile."""
    hw = get_hardware_profile()
    return {
        "gpu_name": hw.gpu_name,
        "gpu_backend": hw.gpu_backend,
        "vram_gb": hw.vram_gb,
        "ram_gb": hw.ram_gb,
        "disk_free_gb": hw.disk_free_gb,
        "cpu_cores": hw.cpu_cores,
        "os": hw.os_name,
        "arch": hw.arch,
    }
