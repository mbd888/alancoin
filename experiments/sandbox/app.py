"""FastAPI application for the Agent Sandbox."""

import asyncio
import sys
from pathlib import Path
from typing import Optional

from fastapi import Depends, FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse

# Ensure harness is importable
_exp_root = str(Path(__file__).parent.parent)
if _exp_root not in sys.path:
    sys.path.insert(0, _exp_root)

from .auth import require_auth
from .estimator import estimate
from .events import event_stream
from .jobs import JobManager
from .models import CreateJobRequest, EstimateRequest, EstimateResponse, JobResponse
from .reports import generate_report
from .scenarios import SCENARIOS, get_scenario

app = FastAPI(
    title="Alancoin Agent Sandbox",
    description="Economic stress-testing service for AI agents",
    version="0.1.0",
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

job_manager = JobManager(max_concurrent=3)


@app.get("/sandbox/health")
async def health():
    return {"status": "ok", "service": "agent-sandbox"}


@app.get("/sandbox/scenarios")
async def list_scenarios():
    return {"scenarios": [s.model_dump() for s in SCENARIOS.values()]}


@app.post("/sandbox/estimate", response_model=EstimateResponse)
async def estimate_job(req: EstimateRequest):
    # Validate scenario exists
    get_scenario(req.scenario)
    return estimate(req)


@app.post("/sandbox/jobs", response_model=JobResponse)
async def create_job(req: CreateJobRequest, _auth=Depends(require_auth)):
    scenario = get_scenario(req.scenario)
    job = job_manager.create_job(req.scenario, req.mock_mode, req.max_runs)

    # Build runner kwargs based on scenario
    runner, kwargs = _build_runner(req)

    # Launch job in background
    task = asyncio.create_task(
        job_manager.run_job(job, runner, generate_report, **kwargs)
    )
    job.task = task

    return job.to_response()


@app.get("/sandbox/jobs/{job_id}", response_model=JobResponse)
async def get_job(job_id: str, _auth=Depends(require_auth)):
    job = job_manager.get_job(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return job.to_response()


@app.delete("/sandbox/jobs/{job_id}")
async def cancel_job(job_id: str, _auth=Depends(require_auth)):
    if not job_manager.cancel_job(job_id):
        raise HTTPException(status_code=404, detail="Job not found or not cancellable")
    return {"status": "cancelled", "job_id": job_id}


@app.get("/sandbox/jobs/{job_id}/events")
async def job_events(job_id: str):
    job = job_manager.get_job(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return StreamingResponse(
        event_stream(job),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


def _build_runner(req: CreateJobRequest):
    """Build an async runner function and kwargs from a job request."""
    import tempfile

    from harness.config import load_config

    output_dir = Path(tempfile.mkdtemp(prefix="sandbox_"))

    if req.scenario in ("budget_efficiency", "price_sensitivity", "delegation_safety"):
        from harness.runners import run_study1

        config = load_config(None)
        if req.mock_mode:
            config.models.buyer.provider = "mock"
            config.models.buyer.name = "mock"
            config.models.seller.provider = "mock"
            config.models.seller.name = "mock"
        elif req.model:
            config.models.buyer.name = req.model
        if req.api_key:
            import os
            os.environ.setdefault("OPENAI_API_KEY", req.api_key)

        # Set conditions based on scenario
        if req.scenario == "budget_efficiency":
            config.study1.conditions = ["competitive_cba"]
        elif req.scenario == "price_sensitivity":
            config.study1.conditions = [
                "monopoly_none", "monopoly_cba",
                "competitive_none", "competitive_cba",
            ]
        elif req.scenario == "delegation_safety":
            config.study1.conditions = ["monopoly_none_sequential"]

        async def runner(**kw):
            return await run_study1(config, output_dir, max_runs=req.max_runs)

        return runner, {}

    elif req.scenario == "adversarial_resistance":
        from harness.runners import run_study2

        config = load_config(None)
        if req.mock_mode:
            config.models.buyer.provider = "mock"
            config.models.buyer.name = "mock"
        elif req.model:
            config.models.buyer.name = req.model
        if req.api_key:
            import os
            os.environ.setdefault("OPENAI_API_KEY", req.api_key)

        async def runner(**kw):
            return await run_study2(config, output_dir, max_runs=req.max_runs)

        return runner, {}

    raise HTTPException(status_code=400, detail=f"Unknown scenario: {req.scenario}")
