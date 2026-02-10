"""Pydantic models for sandbox API requests and responses."""

from datetime import datetime
from enum import Enum
from typing import Any, Dict, List, Optional

from pydantic import BaseModel, Field


class JobStatus(str, Enum):
    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"


class CreateJobRequest(BaseModel):
    """Request to create a new sandbox simulation job."""

    scenario: str = Field(..., description="Scenario ID (e.g., 'budget_efficiency')")
    mock_mode: bool = Field(default=True, description="Use mock LLM (no API costs)")
    max_runs: int = Field(default=5, ge=1, le=100, description="Number of simulation runs")
    model: Optional[str] = Field(default=None, description="Override model (e.g., 'gpt-4o')")
    api_key: Optional[str] = Field(default=None, description="OpenAI/Anthropic API key for real mode")


class JobResponse(BaseModel):
    """Response for a sandbox job."""

    id: str
    scenario: str
    status: JobStatus
    progress: float = 0.0
    created_at: str
    started_at: Optional[str] = None
    completed_at: Optional[str] = None
    mock_mode: bool = True
    max_runs: int = 5
    result: Optional[Dict[str, Any]] = None
    error: Optional[str] = None


class EstimateRequest(BaseModel):
    """Request for a cost/time estimate."""

    scenario: str
    max_runs: int = Field(default=5, ge=1, le=100)
    mock_mode: bool = True
    model: Optional[str] = None


class EstimateResponse(BaseModel):
    """Cost and time estimate for a simulation."""

    scenario: str
    max_runs: int
    mock_mode: bool
    estimated_cost_usd: float
    estimated_time_seconds: float
    estimated_api_calls: int
    model: str
    notes: List[str] = Field(default_factory=list)


class ScenarioInfo(BaseModel):
    """Information about an available scenario."""

    id: str
    name: str
    description: str
    measures: List[str]
    default_runs: int
    estimated_time_mock: str
    estimated_time_real: str


class ReportSummary(BaseModel):
    """Summary section of a sandbox report."""

    overall_score: int = Field(ge=0, le=100)
    grade: str
    headline: str
    key_metrics: Dict[str, Any] = Field(default_factory=dict)


class SandboxReport(BaseModel):
    """Full sandbox evaluation report."""

    job_id: str
    scenario: str
    summary: ReportSummary
    metrics: Dict[str, Any] = Field(default_factory=dict)
    recommendations: List[str] = Field(default_factory=list)
    raw_results: Optional[Dict[str, Any]] = None
