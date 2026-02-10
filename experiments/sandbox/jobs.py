"""Job manager for sandbox simulation jobs."""

import asyncio
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, Coroutine, Dict, Optional

from .models import JobResponse, JobStatus

# Add experiments root to path for harness imports
_exp_root = str(Path(__file__).parent.parent)
if _exp_root not in sys.path:
    sys.path.insert(0, _exp_root)


class Job:
    """A running or completed sandbox job."""

    def __init__(self, job_id: str, scenario: str, mock_mode: bool, max_runs: int):
        self.id = job_id
        self.scenario = scenario
        self.mock_mode = mock_mode
        self.max_runs = max_runs
        self.status = JobStatus.PENDING
        self.progress = 0.0
        self.created_at = datetime.now(timezone.utc)
        self.started_at: Optional[datetime] = None
        self.completed_at: Optional[datetime] = None
        self.result: Optional[Dict[str, Any]] = None
        self.error: Optional[str] = None
        self.task: Optional[asyncio.Task] = None
        self.events: list = []

    def to_response(self) -> JobResponse:
        return JobResponse(
            id=self.id,
            scenario=self.scenario,
            status=self.status,
            progress=self.progress,
            created_at=self.created_at.isoformat(),
            started_at=self.started_at.isoformat() if self.started_at else None,
            completed_at=self.completed_at.isoformat() if self.completed_at else None,
            mock_mode=self.mock_mode,
            max_runs=self.max_runs,
            result=self.result,
            error=self.error,
        )

    def add_event(self, event_type: str, data: dict):
        self.events.append({
            "type": event_type,
            "data": data,
            "timestamp": datetime.now(timezone.utc).isoformat(),
        })


class JobManager:
    """Manages sandbox simulation jobs with bounded concurrency."""

    def __init__(self, max_concurrent: int = 3):
        self.jobs: Dict[str, Job] = {}
        self.semaphore = asyncio.Semaphore(max_concurrent)

    def create_job(self, scenario: str, mock_mode: bool, max_runs: int) -> Job:
        job_id = f"job_{uuid.uuid4().hex[:12]}"
        job = Job(job_id, scenario, mock_mode, max_runs)
        self.jobs[job_id] = job
        return job

    def get_job(self, job_id: str) -> Optional[Job]:
        return self.jobs.get(job_id)

    async def run_job(
        self,
        job: Job,
        runner: Callable[..., Coroutine],
        report_generator: Callable,
        **kwargs,
    ):
        """Run a job with concurrency gating."""
        async with self.semaphore:
            job.status = JobStatus.RUNNING
            job.started_at = datetime.now(timezone.utc)
            job.add_event("started", {"scenario": job.scenario})

            try:
                raw_results = await runner(**kwargs)
                job.progress = 1.0

                # Generate report from raw results
                report = report_generator(job.id, job.scenario, raw_results)
                job.result = report
                job.status = JobStatus.COMPLETED
                job.add_event("completed", {"scenario": job.scenario})
            except asyncio.CancelledError:
                job.status = JobStatus.CANCELLED
                job.add_event("cancelled", {})
            except Exception as e:
                job.status = JobStatus.FAILED
                job.error = str(e)
                job.add_event("failed", {"error": str(e)})
            finally:
                job.completed_at = datetime.now(timezone.utc)

    def cancel_job(self, job_id: str) -> bool:
        job = self.jobs.get(job_id)
        if not job or job.status not in (JobStatus.PENDING, JobStatus.RUNNING):
            return False
        if job.task and not job.task.done():
            job.task.cancel()
        job.status = JobStatus.CANCELLED
        job.completed_at = datetime.now(timezone.utc)
        job.add_event("cancelled", {})
        return True
