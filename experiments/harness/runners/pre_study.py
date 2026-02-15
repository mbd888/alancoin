"""
Pre-study: Reference price calibration.

Establishes baseline willingness-to-pay for each service type
through a series of binary buy/skip decisions at varying price points.

Output: Reference prices derived from logistic regression fit.
"""

import asyncio
import json
import random
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Optional

import numpy as np
from scipy.optimize import curve_fit

from ..config import ExperimentConfig, PreStudyConfig
from ..logging import StructuredLogger, LLMCallEvent
from ..logging.cost_tracker import CostTracker
from ..agents.providers import get_provider, LLMProvider
from ..agents.providers.base import Message
from ..clients.mock_market import ServiceType


@dataclass
class PriceDecision:
    """Record of a single buy/skip decision."""

    service_type: str
    price: float
    reference_price: float
    price_ratio: float  # price / reference
    model: str
    decision: str  # "buy" or "skip"
    bought: bool
    reasoning: str
    latency_ms: float


@dataclass
class PreStudyResults:
    """Results from pre-study."""

    decisions: list[PriceDecision]
    acceptance_rates: dict[str, dict[float, float]]  # service_type -> {price_ratio -> rate}
    fitted_reference_prices: dict[str, float]  # service_type -> reference price
    model_comparison: dict[str, dict]  # model -> {overpay_rate, underpay_rate, etc.}


PRESTUDY_PROMPT = """You are evaluating whether to purchase a service.

Service: {service_type}
Listed Price: ${price:.2f}
Your Budget: ${budget:.2f} per transaction

Task: Decide whether to BUY or SKIP this service based on whether the price seems fair.

Consider:
1. Is this price reasonable for {service_type} service?
2. Is the price within your budget?
3. Would you get good value for this price?

Respond with your reasoning, then your decision in this format:
```json
{{"decision": "buy"}}
```
or
```json
{{"decision": "skip"}}
```
"""


async def run_pre_study(
    config: ExperimentConfig,
    output_dir: Optional[Path] = None,
    mock_mode: bool = False,
) -> PreStudyResults:
    """
    Run pre-study for reference price calibration.

    Args:
        config: Experiment configuration
        output_dir: Output directory for results
        mock_mode: Use mock LLM provider

    Returns:
        PreStudyResults with acceptance rates and reference prices
    """
    print("=" * 60)
    print("PRE-STUDY: Reference Price Calibration")
    print("=" * 60)

    pre_config = config.pre_study
    print(f"Configuration:")
    print(f"  Services: {pre_config.num_services}")
    print(f"  Prices per service: {pre_config.prices_per_service}")
    print(f"  Agents per price: {pre_config.agents_per_price}")
    print(f"  Total decisions: {pre_config.total_decisions}")
    print()

    # Setup output directory
    if output_dir is None:
        output_dir = Path("experiments/results/economic_behavior/pre_study")
    output_dir.mkdir(parents=True, exist_ok=True)

    run_id = f"pre_study_{datetime.utcnow().strftime('%Y%m%d_%H%M%S')}"

    # Initialize logger and cost tracker
    logger = StructuredLogger(output_dir, run_id, study="pre_study")
    cost_tracker = CostTracker(alert_threshold=config.logging.cost_alert_threshold)

    # Initialize providers
    providers = []
    for model_config in config.models:
        if mock_mode:
            provider = get_provider("mock", model=model_config.name)
        else:
            provider = get_provider(
                model_config.provider,
                model=model_config.name,
                cost_per_1k_input=model_config.cost_per_1k_input,
                cost_per_1k_output=model_config.cost_per_1k_output,
            )
        providers.append(provider)
        cost_tracker.register_model(
            model_config.name,
            model_config.provider,
            model_config.cost_per_1k_input,
            model_config.cost_per_1k_output,
        )

    # Define service types and base reference prices
    service_types = list(ServiceType)[:pre_config.num_services]
    base_prices = {
        ServiceType.INFERENCE: 0.50,
        ServiceType.TRANSLATION: 0.30,
        ServiceType.CODE_REVIEW: 1.00,
        ServiceType.DATA_ANALYSIS: 0.75,
        ServiceType.SUMMARIZATION: 0.25,
        ServiceType.EMBEDDING: 0.10,
    }

    decisions: list[PriceDecision] = []
    acceptance_rates: dict[str, dict[float, float]] = {}

    # Generate all decision scenarios
    scenarios = []
    for service_type in service_types:
        base_price = base_prices.get(service_type, 0.50)
        acceptance_rates[service_type.value] = {}

        for multiplier in pre_config.price_multipliers:
            price = base_price * multiplier

            for agent_idx in range(pre_config.agents_per_price):
                # Rotate through models
                provider = providers[agent_idx % len(providers)]

                scenarios.append({
                    "service_type": service_type,
                    "price": price,
                    "base_price": base_price,
                    "multiplier": multiplier,
                    "provider": provider,
                })

    # Shuffle for randomization
    random.shuffle(scenarios)

    print(f"Running {len(scenarios)} decisions...")

    # Run decisions
    for i, scenario in enumerate(scenarios):
        if i % 100 == 0:
            print(f"  Progress: {i}/{len(scenarios)} ({i/len(scenarios)*100:.1f}%)")

        decision = await _make_decision(
            provider=scenario["provider"],
            service_type=scenario["service_type"],
            price=scenario["price"],
            base_price=scenario["base_price"],
            logger=logger,
            cost_tracker=cost_tracker,
        )
        decisions.append(decision)

    logger.flush()

    # Compute acceptance rates
    print("\nComputing acceptance rates...")
    for service_type in service_types:
        type_decisions = [d for d in decisions if d.service_type == service_type.value]

        for multiplier in pre_config.price_multipliers:
            mult_decisions = [
                d for d in type_decisions
                if abs(d.price_ratio - multiplier) < 0.01
            ]
            if mult_decisions:
                rate = sum(1 for d in mult_decisions if d.bought) / len(mult_decisions)
                acceptance_rates[service_type.value][multiplier] = rate

    # Fit reference prices using logistic regression
    print("Fitting reference prices...")
    fitted_prices = {}
    for service_type in service_types:
        type_decisions = [d for d in decisions if d.service_type == service_type.value]
        if type_decisions:
            ref_price = _fit_reference_price(type_decisions, base_prices[service_type])
            fitted_prices[service_type.value] = ref_price

    # Model comparison
    print("Computing model comparison...")
    model_comparison = _compute_model_comparison(decisions, fitted_prices)

    results = PreStudyResults(
        decisions=decisions,
        acceptance_rates=acceptance_rates,
        fitted_reference_prices=fitted_prices,
        model_comparison=model_comparison,
    )

    # Save results
    _save_results(results, output_dir, run_id)

    # Print summary
    _print_summary(results)

    print(f"\nCost summary:")
    print(cost_tracker.report())

    logger.close()

    return results


async def _make_decision(
    provider: LLMProvider,
    service_type: ServiceType,
    price: float,
    base_price: float,
    logger: StructuredLogger,
    cost_tracker: CostTracker,
) -> PriceDecision:
    """Make a single buy/skip decision."""
    import time

    prompt = PRESTUDY_PROMPT.format(
        service_type=service_type.value,
        price=price,
        budget=1.0,  # Standard budget for pre-study
    )

    start = time.perf_counter()
    response = provider.chat(
        system="You are an autonomous purchasing agent evaluating service prices.",
        user_message=prompt,
    )
    latency_ms = (time.perf_counter() - start) * 1000

    # Track cost
    if response.success:
        cost_tracker.record_call(
            provider.model,
            response.input_tokens,
            response.output_tokens,
            latency_ms,
        )

    # Parse decision
    bought = False
    decision_str = "skip"
    content = response.content.lower()

    if '"decision": "buy"' in content or '"decision":"buy"' in content:
        bought = True
        decision_str = "buy"
    elif '"decision": "skip"' in content or '"decision":"skip"' in content:
        bought = False
        decision_str = "skip"
    elif "decision: buy" in content or "i will buy" in content:
        bought = True
        decision_str = "buy"

    # Log event
    event = LLMCallEvent(
        agent_id="pre_study_agent",
        agent_role="buyer",
        model=provider.model,
        provider=provider.provider_name,
        prompt=prompt,
        completion=response.content,
        input_tokens=response.input_tokens,
        output_tokens=response.output_tokens,
        latency_ms=latency_ms,
        cost_usd=response.cost_usd,
        action_type="price_decision",
    )
    logger.log(event)

    return PriceDecision(
        service_type=service_type.value,
        price=price,
        reference_price=base_price,
        price_ratio=price / base_price,
        model=provider.model,
        decision=decision_str,
        bought=bought,
        reasoning=response.content[:500],
        latency_ms=latency_ms,
    )


def _fit_reference_price(
    decisions: list[PriceDecision],
    base_price: float,
) -> float:
    """
    Fit reference price from acceptance data using logistic regression.

    The reference price is the price at which 50% of agents accept.
    """
    if not decisions:
        return base_price

    # Group by price ratio and compute acceptance rates
    ratios = sorted(set(d.price_ratio for d in decisions))
    rates = []

    for ratio in ratios:
        ratio_decisions = [d for d in decisions if abs(d.price_ratio - ratio) < 0.01]
        if ratio_decisions:
            rate = sum(1 for d in ratio_decisions if d.bought) / len(ratio_decisions)
            rates.append(rate)

    if len(ratios) < 3:
        return base_price

    # Fit logistic function
    def logistic(x, k, x0):
        return 1 / (1 + np.exp(k * (x - x0)))

    try:
        ratios_arr = np.array(ratios)
        rates_arr = np.array(rates)

        popt, _ = curve_fit(
            logistic,
            ratios_arr,
            rates_arr,
            p0=[2.0, 1.0],
            bounds=([0.1, 0.1], [10.0, 5.0]),
            maxfev=1000,
        )

        # x0 is the ratio at 50% acceptance
        reference_ratio = popt[1]
        return base_price * reference_ratio

    except Exception:
        # Fall back to base price
        return base_price


def _compute_model_comparison(
    decisions: list[PriceDecision],
    fitted_prices: dict[str, float],
) -> dict[str, dict]:
    """Compute comparison metrics across models."""
    models = set(d.model for d in decisions)
    comparison = {}

    for model in models:
        model_decisions = [d for d in decisions if d.model == model]
        bought_decisions = [d for d in model_decisions if d.bought]

        # Overpay rate: bought when price > 1.2× reference
        overpays = [
            d for d in bought_decisions
            if d.price_ratio > 1.2
        ]

        # Underpay (good deals): bought when price < 0.8× reference
        good_deals = [
            d for d in bought_decisions
            if d.price_ratio < 0.8
        ]

        comparison[model] = {
            "total_decisions": len(model_decisions),
            "buy_rate": len(bought_decisions) / len(model_decisions) if model_decisions else 0,
            "overpay_rate": len(overpays) / len(bought_decisions) if bought_decisions else 0,
            "good_deal_rate": len(good_deals) / len(bought_decisions) if bought_decisions else 0,
            "avg_price_ratio_when_buying": (
                sum(d.price_ratio for d in bought_decisions) / len(bought_decisions)
                if bought_decisions else 0
            ),
        }

    return comparison


def _save_results(results: PreStudyResults, output_dir: Path, run_id: str):
    """Save pre-study results to files."""
    # Save decisions as JSONL
    decisions_path = output_dir / f"{run_id}_decisions.jsonl"
    with open(decisions_path, "w") as f:
        for d in results.decisions:
            f.write(json.dumps({
                "service_type": d.service_type,
                "price": d.price,
                "reference_price": d.reference_price,
                "price_ratio": d.price_ratio,
                "model": d.model,
                "decision": d.decision,
                "bought": d.bought,
                "latency_ms": d.latency_ms,
            }) + "\n")

    # Save summary as JSON
    summary_path = output_dir / f"{run_id}_summary.json"
    with open(summary_path, "w") as f:
        json.dump({
            "run_id": run_id,
            "total_decisions": len(results.decisions),
            "acceptance_rates": results.acceptance_rates,
            "fitted_reference_prices": results.fitted_reference_prices,
            "model_comparison": results.model_comparison,
        }, f, indent=2)

    print(f"\nResults saved to:")
    print(f"  {decisions_path}")
    print(f"  {summary_path}")


def _print_summary(results: PreStudyResults):
    """Print pre-study summary."""
    print("\n" + "=" * 60)
    print("PRE-STUDY RESULTS")
    print("=" * 60)

    print("\nFitted Reference Prices:")
    for service, price in results.fitted_reference_prices.items():
        print(f"  {service}: ${price:.2f}")

    print("\nAcceptance Rates by Price Ratio:")
    for service, rates in results.acceptance_rates.items():
        print(f"\n  {service}:")
        for ratio, rate in sorted(rates.items()):
            bar = "█" * int(rate * 20) + "░" * (20 - int(rate * 20))
            print(f"    {ratio:.2f}×: [{bar}] {rate:.0%}")

    print("\nModel Comparison:")
    for model, metrics in results.model_comparison.items():
        print(f"\n  {model}:")
        print(f"    Buy rate: {metrics['buy_rate']:.1%}")
        print(f"    Overpay rate: {metrics['overpay_rate']:.1%}")
        print(f"    Good deal rate: {metrics['good_deal_rate']:.1%}")
