"""Cost and time estimation for sandbox jobs."""

from .models import EstimateRequest, EstimateResponse

# Average API calls per run by scenario
CALLS_PER_RUN = {
    "budget_efficiency": 20,
    "adversarial_resistance": 15,
    "price_sensitivity": 30,
    "delegation_safety": 25,
}

# Cost per 1K tokens (rough averages)
MODEL_COSTS = {
    "gpt-4o": {"input": 0.005, "output": 0.015},
    "gpt-4o-mini": {"input": 0.00015, "output": 0.0006},
    "claude-3-5-sonnet": {"input": 0.003, "output": 0.015},
    "default": {"input": 0.003, "output": 0.01},
}

# Average tokens per call
AVG_INPUT_TOKENS = 800
AVG_OUTPUT_TOKENS = 400

# Time estimates (seconds per run)
TIME_PER_RUN_MOCK = 2.0
TIME_PER_RUN_REAL = 30.0


def estimate(req: EstimateRequest) -> EstimateResponse:
    """Estimate cost and time for a sandbox job."""
    calls_per_run = CALLS_PER_RUN.get(req.scenario, 20)
    total_calls = calls_per_run * req.max_runs

    model = req.model or "gpt-4o-mini"
    costs = MODEL_COSTS.get(model, MODEL_COSTS["default"])

    notes = []

    if req.mock_mode:
        cost = 0.0
        time_secs = TIME_PER_RUN_MOCK * req.max_runs
        notes.append("Mock mode: no API costs, uses deterministic responses")
    else:
        input_cost = (AVG_INPUT_TOKENS / 1000) * costs["input"] * total_calls
        output_cost = (AVG_OUTPUT_TOKENS / 1000) * costs["output"] * total_calls
        cost = round(input_cost + output_cost, 4)
        time_secs = TIME_PER_RUN_REAL * req.max_runs
        notes.append(f"Estimated based on ~{AVG_INPUT_TOKENS} input + ~{AVG_OUTPUT_TOKENS} output tokens per call")
        notes.append(f"Actual cost depends on model response lengths")

    return EstimateResponse(
        scenario=req.scenario,
        max_runs=req.max_runs,
        mock_mode=req.mock_mode,
        estimated_cost_usd=cost,
        estimated_time_seconds=round(time_secs, 1),
        estimated_api_calls=total_calls,
        model=model,
        notes=notes,
    )
