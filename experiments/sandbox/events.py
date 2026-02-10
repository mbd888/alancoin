"""Server-Sent Events (SSE) streaming for job progress."""

import asyncio
import json
from typing import AsyncGenerator

from .jobs import Job, JobStatus


async def event_stream(job: Job) -> AsyncGenerator[str, None]:
    """Generate SSE events for a job's progress."""
    last_event_idx = 0
    last_progress = -1.0

    while True:
        # Send any new events
        while last_event_idx < len(job.events):
            event = job.events[last_event_idx]
            yield _format_sse(event["type"], event["data"])
            last_event_idx += 1

        # Send progress updates
        if job.progress != last_progress:
            last_progress = job.progress
            yield _format_sse("progress", {
                "progress": job.progress,
                "status": job.status.value,
            })

        # Check if job is done
        if job.status in (JobStatus.COMPLETED, JobStatus.FAILED, JobStatus.CANCELLED):
            yield _format_sse("done", {
                "status": job.status.value,
                "has_result": job.result is not None,
            })
            return

        await asyncio.sleep(0.5)


def _format_sse(event_type: str, data: dict) -> str:
    """Format a Server-Sent Event."""
    return f"event: {event_type}\ndata: {json.dumps(data)}\n\n"
