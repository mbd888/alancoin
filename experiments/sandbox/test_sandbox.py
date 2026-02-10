"""Tests for the Agent Sandbox package.

Run with: python3 -m pytest experiments/sandbox/test_sandbox.py -v
"""

import asyncio
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import List
from unittest.mock import patch

import pytest

# Ensure sandbox package is importable without going through experiments/__init__.py
_sandbox_parent = str(Path(__file__).parent.parent)
if _sandbox_parent not in sys.path:
    sys.path.insert(0, _sandbox_parent)

from sandbox.estimator import estimate
from sandbox.jobs import Job, JobManager, JobStatus
from sandbox.models import CreateJobRequest, EstimateRequest
from sandbox.reports import (
    _score_to_grade,
    generate_report,
)
from sandbox.scenarios import SCENARIOS, get_scenario


# --- Scenarios ---


def test_get_scenario_valid():
    for sid in ("budget_efficiency", "adversarial_resistance", "price_sensitivity", "delegation_safety"):
        s = get_scenario(sid)
        assert s.id == sid
        assert len(s.measures) > 0


def test_get_scenario_invalid():
    with pytest.raises(ValueError, match="Unknown scenario"):
        get_scenario("nonexistent")


def test_all_scenarios_have_required_fields():
    for sid, s in SCENARIOS.items():
        assert s.name
        assert s.description
        assert s.default_runs > 0


# --- Estimator ---


def test_estimate_mock_mode():
    req = EstimateRequest(scenario="budget_efficiency", max_runs=5, mock_mode=True)
    resp = estimate(req)
    assert resp.estimated_cost_usd == 0.0
    assert resp.estimated_time_seconds > 0
    assert resp.estimated_api_calls == 100  # 20 calls/run * 5 runs
    assert any("Mock mode" in n for n in resp.notes)


def test_estimate_real_mode():
    req = EstimateRequest(scenario="budget_efficiency", max_runs=5, mock_mode=False)
    resp = estimate(req)
    assert resp.estimated_cost_usd > 0
    assert resp.estimated_time_seconds > 0
    assert resp.model == "gpt-4o-mini"  # default model


def test_estimate_custom_model():
    req = EstimateRequest(scenario="budget_efficiency", max_runs=3, mock_mode=False, model="gpt-4o")
    resp = estimate(req)
    assert resp.model == "gpt-4o"
    assert resp.estimated_cost_usd > 0


def test_estimate_unknown_scenario_uses_default_calls():
    req = EstimateRequest(scenario="unknown_scenario", max_runs=1, mock_mode=True)
    resp = estimate(req)
    assert resp.estimated_api_calls == 20  # default fallback


# --- Reports ---


def test_score_to_grade():
    assert _score_to_grade(95) == "A"
    assert _score_to_grade(90) == "A"
    assert _score_to_grade(85) == "B"
    assert _score_to_grade(80) == "B"
    assert _score_to_grade(75) == "C"
    assert _score_to_grade(70) == "C"
    assert _score_to_grade(65) == "D"
    assert _score_to_grade(60) == "D"
    assert _score_to_grade(55) == "F"
    assert _score_to_grade(0) == "F"


@dataclass
class MockRunResult:
    transactions_attempted: int = 10
    transactions_accepted: int = 8
    transactions_rejected: int = 2
    avg_price_ratio: float = 1.05
    budget_utilization: float = 0.75
    overpayment_rate: float = 0.1
    task_completion_rate: float = 0.9
    is_sequential: bool = False
    sequence_completed: bool = False
    exploited: bool = False
    detection_shown: bool = False
    cba_blocked: bool = False


@dataclass
class MockResults:
    runs: List[MockRunResult]


def test_budget_efficiency_report():
    runs = [
        MockRunResult(avg_price_ratio=1.05, budget_utilization=0.8, overpayment_rate=0.1),
        MockRunResult(avg_price_ratio=1.10, budget_utilization=0.7, overpayment_rate=0.15),
    ]
    report = generate_report("job_1", "budget_efficiency", MockResults(runs=runs))

    assert report["job_id"] == "job_1"
    assert report["scenario"] == "budget_efficiency"
    assert 0 <= report["summary"]["overall_score"] <= 100
    assert report["summary"]["grade"] in ("A", "B", "C", "D", "F")
    assert "price_efficiency" in report["summary"]["key_metrics"]
    assert len(report["recommendations"]) > 0


def test_budget_efficiency_report_empty():
    report = generate_report("job_2", "budget_efficiency", MockResults(runs=[]))
    assert report["summary"]["overall_score"] == 0
    assert report["summary"]["grade"] == "F"


def test_adversarial_resistance_report():
    runs = [
        MockRunResult(exploited=True, detection_shown=True, cba_blocked=False),
        MockRunResult(exploited=False, detection_shown=True, cba_blocked=True),
        MockRunResult(exploited=False, detection_shown=False, cba_blocked=False),
    ]
    report = generate_report("job_3", "adversarial_resistance", MockResults(runs=runs))

    assert report["summary"]["key_metrics"]["exploitation_rate"] == pytest.approx(1 / 3, abs=0.01)
    assert report["summary"]["key_metrics"]["detection_rate"] == pytest.approx(2 / 3, abs=0.01)
    assert len(report["recommendations"]) > 0


def test_price_sensitivity_report():
    runs = [
        MockRunResult(avg_price_ratio=1.0, transactions_rejected=1, transactions_attempted=10),
        MockRunResult(avg_price_ratio=1.1, transactions_rejected=2, transactions_attempted=10),
    ]
    report = generate_report("job_4", "price_sensitivity", MockResults(runs=runs))

    assert "mean_price_ratio" in report["summary"]["key_metrics"]
    assert "fair_price_rate" in report["summary"]["key_metrics"]


def test_delegation_safety_report():
    runs = [
        MockRunResult(is_sequential=True, sequence_completed=True, budget_utilization=0.8, task_completion_rate=0.95),
        MockRunResult(is_sequential=True, sequence_completed=False, budget_utilization=0.95, task_completion_rate=0.5),
    ]
    report = generate_report("job_5", "delegation_safety", MockResults(runs=runs))

    assert "sequence_completion_rate" in report["summary"]["key_metrics"]
    assert "budget_remaining" in report["summary"]["key_metrics"]


def test_generic_report():
    report = generate_report("job_6", "unknown_scenario", None)
    assert report["summary"]["overall_score"] == 50
    assert report["summary"]["grade"] == "C"


# --- Jobs ---


def test_job_creation():
    job = Job("job_123", "budget_efficiency", mock_mode=True, max_runs=5)
    assert job.id == "job_123"
    assert job.status == JobStatus.PENDING
    assert job.progress == 0.0
    assert job.mock_mode is True
    assert job.max_runs == 5


def test_job_to_response():
    job = Job("job_123", "budget_efficiency", mock_mode=True, max_runs=5)
    resp = job.to_response()
    assert resp.id == "job_123"
    assert resp.scenario == "budget_efficiency"
    assert resp.status == JobStatus.PENDING


def test_job_add_event():
    job = Job("job_123", "budget_efficiency", mock_mode=True, max_runs=5)
    job.add_event("started", {"scenario": "budget_efficiency"})
    assert len(job.events) == 1
    assert job.events[0]["type"] == "started"
    assert "timestamp" in job.events[0]


def test_job_manager_create_and_get():
    mgr = JobManager(max_concurrent=2)
    job = mgr.create_job("budget_efficiency", mock_mode=True, max_runs=3)
    assert job.id.startswith("job_")
    assert mgr.get_job(job.id) is job
    assert mgr.get_job("nonexistent") is None


def test_job_manager_cancel():
    mgr = JobManager()
    job = mgr.create_job("budget_efficiency", mock_mode=True, max_runs=3)
    assert mgr.cancel_job(job.id) is True
    assert job.status == JobStatus.CANCELLED
    assert job.completed_at is not None


def test_job_manager_cancel_nonexistent():
    mgr = JobManager()
    assert mgr.cancel_job("nonexistent") is False


def test_job_manager_cancel_already_completed():
    mgr = JobManager()
    job = mgr.create_job("budget_efficiency", mock_mode=True, max_runs=3)
    job.status = JobStatus.COMPLETED
    assert mgr.cancel_job(job.id) is False


@pytest.mark.asyncio
async def test_job_manager_run_success():
    mgr = JobManager()
    job = mgr.create_job("budget_efficiency", mock_mode=True, max_runs=1)

    async def mock_runner(**kw):
        return MockResults(runs=[MockRunResult()])

    await mgr.run_job(job, mock_runner, generate_report)
    assert job.status == JobStatus.COMPLETED
    assert job.result is not None
    assert job.result["job_id"] == job.id


@pytest.mark.asyncio
async def test_job_manager_run_failure():
    mgr = JobManager()
    job = mgr.create_job("budget_efficiency", mock_mode=True, max_runs=1)

    async def failing_runner(**kw):
        raise RuntimeError("test error")

    await mgr.run_job(job, failing_runner, generate_report)
    assert job.status == JobStatus.FAILED
    assert "test error" in job.error


# --- Auth ---


def test_auth_demo_mode():
    """When SANDBOX_API_KEY is empty, auth is skipped."""
    from sandbox.auth import require_auth

    with patch("sandbox.auth.SANDBOX_API_KEY", ""):
        result = require_auth(x_api_key=None)
        assert result is None


def test_auth_valid_key():
    from sandbox.auth import require_auth

    with patch("sandbox.auth.SANDBOX_API_KEY", "test-key"):
        result = require_auth(x_api_key="test-key")
        assert result == "test-key"


def test_auth_invalid_key():
    from fastapi import HTTPException
    from sandbox.auth import require_auth

    with patch("sandbox.auth.SANDBOX_API_KEY", "test-key"):
        with pytest.raises(HTTPException) as exc_info:
            require_auth(x_api_key="wrong-key")
        assert exc_info.value.status_code == 401


def test_auth_missing_key():
    from fastapi import HTTPException
    from sandbox.auth import require_auth

    with patch("sandbox.auth.SANDBOX_API_KEY", "test-key"):
        with pytest.raises(HTTPException) as exc_info:
            require_auth(x_api_key=None)
        assert exc_info.value.status_code == 401


# --- Events ---


@pytest.mark.asyncio
async def test_event_stream_completed_job():
    from sandbox.events import event_stream

    job = Job("job_test", "budget_efficiency", mock_mode=True, max_runs=1)
    job.status = JobStatus.COMPLETED
    job.result = {"summary": {}}
    job.add_event("completed", {"scenario": "budget_efficiency"})

    events = []
    async for ev in event_stream(job):
        events.append(ev)

    assert len(events) >= 2  # at least the event + done
    assert "event: completed" in events[0]
    assert "event: done" in events[-1]
