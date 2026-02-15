"""
Token and API cost tracking for experiment runs.

Aggregates costs across multiple LLM calls and provides
alerts when spending exceeds thresholds.
"""

import threading
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Optional


class CostLimitExceeded(Exception):
    """
    Raised when cost exceeds the hard limit.

    Attributes:
        total_cost: Total cost at the time of exceeding the limit.
        hard_limit: The limit that was exceeded.
        partial_results: Any partial results saved before aborting.
    """

    def __init__(
        self,
        total_cost: float,
        hard_limit: float,
        partial_results: Optional[dict] = None,
    ):
        self.total_cost = total_cost
        self.hard_limit = hard_limit
        self.partial_results = partial_results or {}
        super().__init__(
            f"Cost limit exceeded: ${total_cost:.2f} >= ${hard_limit:.2f}. "
            "Partial results saved."
        )


@dataclass
class ModelCosts:
    """Cost tracking for a single model."""

    model: str
    provider: str
    cost_per_1k_input: float
    cost_per_1k_output: float

    total_input_tokens: int = 0
    total_output_tokens: int = 0
    total_calls: int = 0
    total_cost_usd: float = 0.0
    total_latency_ms: float = 0.0

    def add_call(
        self,
        input_tokens: int,
        output_tokens: int,
        latency_ms: float = 0.0,
    ) -> float:
        """
        Record an LLM call and return the cost.

        Returns:
            Cost of this call in USD
        """
        self.total_input_tokens += input_tokens
        self.total_output_tokens += output_tokens
        self.total_calls += 1
        self.total_latency_ms += latency_ms

        cost = self.compute_cost(input_tokens, output_tokens)
        self.total_cost_usd += cost
        return cost

    def compute_cost(self, input_tokens: int, output_tokens: int) -> float:
        """Compute cost for given token counts."""
        input_cost = (input_tokens / 1000) * self.cost_per_1k_input
        output_cost = (output_tokens / 1000) * self.cost_per_1k_output
        return input_cost + output_cost

    @property
    def avg_latency_ms(self) -> float:
        """Average latency per call."""
        if self.total_calls == 0:
            return 0.0
        return self.total_latency_ms / self.total_calls

    @property
    def avg_tokens_per_call(self) -> float:
        """Average total tokens per call."""
        if self.total_calls == 0:
            return 0.0
        return (self.total_input_tokens + self.total_output_tokens) / self.total_calls


@dataclass
class CostAlert:
    """Record of a cost threshold alert."""

    timestamp: str
    threshold: float
    current_cost: float
    model: Optional[str]
    message: str


class CostTracker:
    """
    Thread-safe cost tracker for experiment runs.

    Aggregates costs across all models and provides alerts
    when spending exceeds configurable thresholds.
    """

    def __init__(
        self,
        alert_threshold: float = 10.0,
        hard_limit: Optional[float] = None,
    ):
        """
        Initialize cost tracker.

        Args:
            alert_threshold: USD threshold for warning alerts
            hard_limit: USD limit that will raise an exception if exceeded
        """
        self.alert_threshold = alert_threshold
        self.hard_limit = hard_limit

        self._models: dict[str, ModelCosts] = {}
        self._alerts: list[CostAlert] = []
        self._lock = threading.Lock()
        self._alert_triggered = False

    def register_model(
        self,
        model: str,
        provider: str,
        cost_per_1k_input: float,
        cost_per_1k_output: float,
    ):
        """Register a model with its cost structure."""
        with self._lock:
            self._models[model] = ModelCosts(
                model=model,
                provider=provider,
                cost_per_1k_input=cost_per_1k_input,
                cost_per_1k_output=cost_per_1k_output,
            )

    def record_call(
        self,
        model: str,
        input_tokens: int,
        output_tokens: int,
        latency_ms: float = 0.0,
    ) -> float:
        """
        Record an LLM call.

        Args:
            model: Model name
            input_tokens: Number of input tokens
            output_tokens: Number of output tokens
            latency_ms: Call latency in milliseconds

        Returns:
            Cost of this call in USD

        Raises:
            RuntimeError: If hard limit is exceeded
        """
        with self._lock:
            if model not in self._models:
                # Use default costs if model not registered
                self._models[model] = ModelCosts(
                    model=model,
                    provider="unknown",
                    cost_per_1k_input=0.01,  # Conservative default
                    cost_per_1k_output=0.03,
                )

            # Pre-check: compute cost BEFORE committing to prevent overshoot
            pending_cost = self._models[model].compute_cost(input_tokens, output_tokens)
            projected_total = self.total_cost_usd + pending_cost

            if self.hard_limit and projected_total >= self.hard_limit:
                raise CostLimitExceeded(projected_total, self.hard_limit)

            # Safe to commit — we're under limit
            cost = self._models[model].add_call(input_tokens, output_tokens, latency_ms)

            # Check soft alert threshold (non-fatal)
            total = self.total_cost_usd
            self._check_alert(total, model)

            return cost

    def _check_alert(self, total_cost: float, model: str):
        """Check soft alert threshold. Must hold lock."""
        if not self._alert_triggered and total_cost >= self.alert_threshold:
            self._alert_triggered = True
            alert = CostAlert(
                timestamp=datetime.now(timezone.utc).isoformat(),
                threshold=self.alert_threshold,
                current_cost=total_cost,
                model=model,
                message=f"Cost alert: ${total_cost:.2f} exceeds threshold ${self.alert_threshold:.2f}",
            )
            self._alerts.append(alert)

    @property
    def total_cost_usd(self) -> float:
        """Total cost across all models."""
        return sum(m.total_cost_usd for m in self._models.values())

    @property
    def total_calls(self) -> int:
        """Total number of LLM calls."""
        return sum(m.total_calls for m in self._models.values())

    @property
    def total_tokens(self) -> int:
        """Total tokens (input + output) across all models."""
        return sum(
            m.total_input_tokens + m.total_output_tokens
            for m in self._models.values()
        )

    def get_model_summary(self, model: str) -> Optional[ModelCosts]:
        """Get cost summary for a specific model."""
        with self._lock:
            return self._models.get(model)

    def get_all_summaries(self) -> dict[str, ModelCosts]:
        """Get cost summaries for all models."""
        with self._lock:
            return dict(self._models)

    def get_alerts(self) -> list[CostAlert]:
        """Get all triggered alerts."""
        with self._lock:
            return list(self._alerts)

    def estimate_cost(
        self,
        num_runs: int,
        llm_calls_per_run: int = 15,
        avg_input_tokens: int = 500,
        avg_output_tokens: int = 200,
    ) -> float:
        """
        Estimate total cost for a study run.

        Args:
            num_runs: Number of experiment runs.
            llm_calls_per_run: Average LLM calls per run.
            avg_input_tokens: Average input tokens per call.
            avg_output_tokens: Average output tokens per call.

        Returns:
            Estimated cost in USD.
        """
        with self._lock:
            if not self._models:
                return 0.0
            total = 0.0
            num_models = len(self._models)
            calls_per_model = (num_runs * llm_calls_per_run) / max(num_models, 1)
            for costs in self._models.values():
                total += costs.compute_cost(avg_input_tokens, avg_output_tokens) * calls_per_model
            return total

    def progress_report(self, current_run: int, total_runs: int) -> str:
        """
        Generate a progress cost summary.

        Args:
            current_run: Current run number.
            total_runs: Total planned runs.

        Returns:
            One-line progress string.
        """
        pct = (current_run / total_runs * 100) if total_runs > 0 else 0
        limit_str = f" / ${self.hard_limit:.2f} limit" if self.hard_limit else ""
        return (
            f"  [{current_run}/{total_runs} runs ({pct:.0f}%)] "
            f"Cost: ${self.total_cost_usd:.4f}{limit_str}"
        )

    def report(self) -> str:
        """Generate a human-readable cost report."""
        with self._lock:
            lines = [
                "=" * 60,
                "COST REPORT",
                "=" * 60,
                "",
            ]

            for model, costs in sorted(self._models.items()):
                lines.append(f"Model: {model}")
                lines.append(f"  Provider: {costs.provider}")
                lines.append(f"  Calls: {costs.total_calls}")
                lines.append(f"  Input tokens: {costs.total_input_tokens:,}")
                lines.append(f"  Output tokens: {costs.total_output_tokens:,}")
                lines.append(f"  Total cost: ${costs.total_cost_usd:.4f}")
                lines.append(f"  Avg latency: {costs.avg_latency_ms:.1f}ms")
                lines.append("")

            lines.append("-" * 60)
            lines.append(f"TOTAL COST: ${self.total_cost_usd:.4f}")
            lines.append(f"TOTAL CALLS: {self.total_calls}")
            lines.append(f"TOTAL TOKENS: {self.total_tokens:,}")

            if self._alerts:
                lines.append("")
                lines.append("ALERTS:")
                for alert in self._alerts:
                    lines.append(f"  - {alert.message}")

            return "\n".join(lines)

    def to_dict(self) -> dict:
        """Convert to dictionary for JSON serialization."""
        with self._lock:
            return {
                "total_cost_usd": self.total_cost_usd,
                "total_calls": self.total_calls,
                "total_tokens": self.total_tokens,
                "alert_threshold": self.alert_threshold,
                "hard_limit": self.hard_limit,
                "models": {
                    model: {
                        "provider": costs.provider,
                        "total_calls": costs.total_calls,
                        "total_input_tokens": costs.total_input_tokens,
                        "total_output_tokens": costs.total_output_tokens,
                        "total_cost_usd": costs.total_cost_usd,
                        "avg_latency_ms": costs.avg_latency_ms,
                    }
                    for model, costs in self._models.items()
                },
                "alerts": [
                    {
                        "timestamp": a.timestamp,
                        "threshold": a.threshold,
                        "current_cost": a.current_cost,
                        "message": a.message,
                    }
                    for a in self._alerts
                ],
            }
