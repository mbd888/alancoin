"""Preset scenarios mapping to existing experiment runners."""

from .models import ScenarioInfo

SCENARIOS = {
    "budget_efficiency": ScenarioInfo(
        id="budget_efficiency",
        name="Budget Efficiency",
        description="Tests how efficiently agents spend within budget constraints. "
                    "Runs competitive market with CBA enforcement and measures "
                    "price ratios, budget utilization, and overpayment rates.",
        measures=["price_ratio", "budget_utilization", "overpayment_rate"],
        default_runs=5,
        estimated_time_mock="10s",
        estimated_time_real="2-5min",
    ),
    "adversarial_resistance": ScenarioInfo(
        id="adversarial_resistance",
        name="Adversarial Resistance",
        description="Tests agent resilience against overcharging, non-delivery, "
                    "bait-and-switch, and injection attacks. Measures exploitation "
                    "rates and CBA defense effectiveness.",
        measures=["exploitation_rate", "detection_rate", "cba_block_rate"],
        default_runs=5,
        estimated_time_mock="15s",
        estimated_time_real="3-8min",
    ),
    "price_sensitivity": ScenarioInfo(
        id="price_sensitivity",
        name="Price Sensitivity",
        description="Tests how agents respond to monopoly vs competitive pricing "
                    "with and without CBA constraints. Measures price acceptance "
                    "patterns and fair-price rates.",
        measures=["mean_price_ratio", "fair_price_rate", "rejection_rate"],
        default_runs=5,
        estimated_time_mock="15s",
        estimated_time_real="3-6min",
    ),
    "delegation_safety": ScenarioInfo(
        id="delegation_safety",
        name="Delegation Safety",
        description="Tests multi-step task planning with constrained total budget. "
                    "Agents must complete a sequence of dependent tasks without "
                    "overspending.",
        measures=["sequence_completion_rate", "budget_remaining", "task_order_correct"],
        default_runs=5,
        estimated_time_mock="10s",
        estimated_time_real="2-5min",
    ),
}


def get_scenario(scenario_id: str) -> ScenarioInfo:
    """Get a scenario by ID."""
    if scenario_id not in SCENARIOS:
        raise ValueError(f"Unknown scenario: {scenario_id}. Available: {list(SCENARIOS.keys())}")
    return SCENARIOS[scenario_id]
