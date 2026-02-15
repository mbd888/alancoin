"""
Study 3: Reputation dynamics (exploratory).

Investigates how LLM agents learn to use and respond to reputation
signals over multiple rounds of interaction.
"""

import asyncio
import json
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Optional

from ..config import ExperimentConfig, Study3Config
from ..logging import StructuredLogger
from ..logging.cost_tracker import CostTracker
from ..agents.buyer import BuyerAgent
from ..agents.seller import SellerAgent
from ..agents.providers import get_provider
from ..clients.mock_market import MockMarket, ServiceType


@dataclass
class Study3RoundResult:
    """Results from a single round in Study 3."""

    round_number: int
    transactions: int
    avg_price: float
    seller_chosen: str
    seller_reputation: float
    buyer_mentioned_reputation: bool
    reasoning_excerpt: str


@dataclass
class Study3RunResult:
    """Results from a single Study 3 run."""

    run_id: int
    condition: str
    model: str
    rounds: list[Study3RoundResult]

    # Aggregate metrics
    reputation_influence_score: float = 0.0  # How much reputation affected choices
    learning_curve_slope: float = 0.0  # Did agent improve over time


@dataclass
class Study3Results:
    """Aggregated Study 3 results."""

    runs: list[Study3RunResult]
    by_condition: dict[str, dict]
    by_model: dict[str, dict]
    reputation_learning_curves: dict[str, list[float]]


async def run_study3(
    config: ExperimentConfig,
    output_dir: Optional[Path] = None,
    mock_mode: bool = False,
    max_runs: Optional[int] = None,
) -> Study3Results:
    """
    Run Study 3: Reputation dynamics (exploratory).

    Args:
        config: Experiment configuration
        output_dir: Output directory for results
        mock_mode: Use mock LLM provider
        max_runs: Maximum number of runs (for testing)

    Returns:
        Study3Results with all run data
    """
    print("=" * 60)
    print("STUDY 3: Reputation Dynamics (Exploratory)")
    print("=" * 60)

    study_config = config.study3
    print(f"Conditions: {study_config.conditions}")
    print(f"Rounds per run: {study_config.rounds_per_run}")
    print(f"Runs per condition: {study_config.runs_per_condition}")
    print()

    # Setup output directory
    if output_dir is None:
        output_dir = Path("experiments/results/economic_behavior/study3")
    output_dir.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.utcnow().strftime("%Y%m%d_%H%M%S")
    run_id_prefix = f"study3_{timestamp}"

    # Generate design
    design = _generate_study3_design(config)

    if max_runs:
        design = design[:max_runs]

    print(f"Running {len(design)} experiment runs...")

    # Initialize cost tracker
    cost_tracker = CostTracker(
        alert_threshold=config.logging.cost_alert_threshold,
        hard_limit=50.0,
    )

    for model_config in config.models:
        cost_tracker.register_model(
            model_config.name,
            model_config.provider,
            model_config.cost_per_1k_input,
            model_config.cost_per_1k_output,
        )

    # Run experiments
    results: list[Study3RunResult] = []

    for i, run_config in enumerate(design):
        print(f"\n[{i+1}/{len(design)}] {run_config['condition']} "
              f"(model: {run_config['model'][:20]}...)")

        try:
            run_result = await _run_reputation_trial(
                run_config=run_config,
                experiment_config=config,
                output_dir=output_dir,
                run_id_prefix=run_id_prefix,
                mock_mode=mock_mode,
                cost_tracker=cost_tracker,
            )
            results.append(run_result)

            print(f"    Rounds completed: {len(run_result.rounds)}")
            print(f"    Reputation influence: {run_result.reputation_influence_score:.2f}")

        except Exception as e:
            print(f"    ERROR: {e}")
            continue

    # Aggregate results
    study_results = _aggregate_study3_results(results)

    # Save results
    _save_study3_results(study_results, output_dir, run_id_prefix)

    # Print summary
    _print_study3_summary(study_results)

    print(f"\nCost summary:")
    print(cost_tracker.report())

    return study_results


def _generate_study3_design(config: ExperimentConfig) -> list[dict]:
    """Generate Study 3 experiment design."""
    study_config = config.study3
    design = []
    run_id = 0

    model_names = [m.name for m in config.models]

    for condition in study_config.conditions:
        for _ in range(study_config.runs_per_condition):
            for model in model_names:
                design.append({
                    "run_id": run_id,
                    "condition": condition,
                    "model": model,
                    "rounds": study_config.rounds_per_run,
                })
                run_id += 1

    return design


async def _run_reputation_trial(
    run_config: dict,
    experiment_config: ExperimentConfig,
    output_dir: Path,
    run_id_prefix: str,
    mock_mode: bool,
    cost_tracker: CostTracker,
) -> Study3RunResult:
    """Run a single reputation dynamics trial."""
    run_id = run_config["run_id"]
    condition = run_config["condition"]
    model_name = run_config["model"]
    num_rounds = run_config["rounds"]

    # Initialize logger
    logger = StructuredLogger(
        output_dir,
        f"{run_id_prefix}_run{run_id:03d}",
        study="study3",
    )

    # Create market
    market = MockMarket(
        seed=experiment_config.random_seed + run_id,
        cba_enabled=True,
    )

    # Create sellers with different reputation profiles
    sellers = []
    seller_reputations = [
        ("HighRep", 90.0, 0.95),   # High reputation, good quality
        ("MedRep", 60.0, 0.75),    # Medium reputation, ok quality
        ("LowRep", 30.0, 0.50),    # Low reputation, poor quality
    ]

    for name, rep, quality in seller_reputations:
        seller_agent = market.create_agent(
            name=name,
            role="seller",
            balance=0.0,
        )
        seller_agent.reputation_score = rep

        # Add service
        service = market.add_service(
            seller_id=seller_agent.id,
            service_type=ServiceType.INFERENCE,
            name=f"{name} Inference",
            description=f"Inference service from {name}",
            price=0.50,  # Same price for all
            quality_score=quality,
        )

        sellers.append({
            "agent": seller_agent,
            "service": service,
            "quality": quality,
        })

    # Create buyer
    buyer_provider = _get_provider(experiment_config, model_name, mock_mode)

    # Adjust system prompt based on condition
    if condition == "no_reputation":
        # Don't mention reputation in prompt
        system_addendum = ""
    elif condition == "visible_reputation":
        system_addendum = "\n\nNote: Seller reputation scores are visible and may indicate service quality."
    else:  # reputation_with_history
        system_addendum = "\n\nNote: You can see seller reputation and your own transaction history. Learn from past experiences."

    buyer = BuyerAgent(
        agent_id=f"buyer_{run_id}",
        provider=buyer_provider,
        budget=100.0,  # Higher budget for multi-round
        max_per_tx=1.0,
        max_per_day=50.0,
        tasks=["Purchase inference services, optimizing for quality"],
        logger=logger,
        cost_tracker=cost_tracker,
    )

    buyer_agent = market.create_agent(
        name="Buyer",
        role="buyer",
        balance=100.0,
        max_per_tx=1.0,
    )

    # Run rounds
    round_results = []
    transaction_history = []

    for round_num in range(1, num_rounds + 1):
        # Build observation based on condition
        services_info = []
        for s in sellers:
            info = {
                "id": s["service"].id,
                "name": s["service"].name,
                "price": s["service"].price,
                "seller_id": s["agent"].id,
            }

            # Include reputation if condition allows
            if condition in ["visible_reputation", "reputation_with_history"]:
                info["seller_reputation"] = s["agent"].reputation_score

            services_info.append(info)

        observation = {
            "phase": "selection",
            "market_round": round_num,
            "services": services_info,
        }

        # Include history if condition allows
        if condition == "reputation_with_history" and transaction_history:
            observation["transaction_history"] = transaction_history[-5:]  # Last 5

        # Get buyer decision
        action = await buyer.act(observation)

        # Process decision
        selected_service = action.get("service_id", "")
        selected_seller = None

        for s in sellers:
            if s["service"].id == selected_service:
                selected_seller = s
                break

        if selected_seller is None:
            # Default to first seller if none selected
            selected_seller = sellers[0]
            selected_service = selected_seller["service"].id

        # Execute transaction
        tx = market.transact(
            sender_id=buyer_agent.id,
            recipient_id=selected_seller["agent"].id,
            amount=selected_seller["service"].price,
            service_id=selected_service,
        )

        # Record result
        reasoning = buyer.state.messages[-1].content if buyer.state.messages else ""
        mentioned_rep = "reputation" in reasoning.lower() or "rating" in reasoning.lower()

        round_result = Study3RoundResult(
            round_number=round_num,
            transactions=1 if tx.status == "accepted" else 0,
            avg_price=selected_seller["service"].price if tx.status == "accepted" else 0,
            seller_chosen=selected_seller["agent"].name,
            seller_reputation=selected_seller["agent"].reputation_score,
            buyer_mentioned_reputation=mentioned_rep,
            reasoning_excerpt=reasoning[:200],
        )
        round_results.append(round_result)

        # Add to history
        if tx.status == "accepted":
            transaction_history.append({
                "round": round_num,
                "seller": selected_seller["agent"].name,
                "quality_received": selected_seller["quality"],
                "price": selected_seller["service"].price,
            })

    # Compute metrics
    reputation_influence = _compute_reputation_influence(round_results, sellers)
    learning_slope = _compute_learning_slope(round_results, sellers)

    logger.close()

    return Study3RunResult(
        run_id=run_id,
        condition=condition,
        model=model_name,
        rounds=round_results,
        reputation_influence_score=reputation_influence,
        learning_curve_slope=learning_slope,
    )


def _get_provider(config: ExperimentConfig, model_name: str, mock_mode: bool):
    """Get provider for model."""
    if mock_mode:
        return get_provider("mock", model=model_name)

    model_config = config.get_model(model_name)
    if model_config:
        return get_provider(
            model_config.provider,
            model=model_config.name,
            cost_per_1k_input=model_config.cost_per_1k_input,
            cost_per_1k_output=model_config.cost_per_1k_output,
        )
    return get_provider("mock", model=model_name)


def _compute_reputation_influence(rounds: list[Study3RoundResult], sellers: list) -> float:
    """Compute how much reputation influenced decisions."""
    # Simple metric: correlation between chosen seller reputation and choice frequency
    high_rep_choices = sum(1 for r in rounds if r.seller_reputation >= 80)
    total = len(rounds)
    return high_rep_choices / total if total > 0 else 0


def _compute_learning_slope(rounds: list[Study3RoundResult], sellers: list) -> float:
    """Compute learning curve slope (did choices improve over time)."""
    if len(rounds) < 10:
        return 0.0

    # Compare first half to second half
    mid = len(rounds) // 2
    first_half = rounds[:mid]
    second_half = rounds[mid:]

    first_rep_avg = sum(r.seller_reputation for r in first_half) / len(first_half)
    second_rep_avg = sum(r.seller_reputation for r in second_half) / len(second_half)

    return second_rep_avg - first_rep_avg


def _aggregate_study3_results(runs: list[Study3RunResult]) -> Study3Results:
    """Aggregate Study 3 results."""
    by_condition = {}
    by_model = {}
    learning_curves = {}

    for run in runs:
        if run.condition not in by_condition:
            by_condition[run.condition] = []
        by_condition[run.condition].append(run)

        if run.model not in by_model:
            by_model[run.model] = []
        by_model[run.model].append(run)

    def aggregate(run_list):
        if not run_list:
            return {}
        return {
            "n": len(run_list),
            "avg_reputation_influence": sum(r.reputation_influence_score for r in run_list) / len(run_list),
            "avg_learning_slope": sum(r.learning_curve_slope for r in run_list) / len(run_list),
            "mentioned_reputation_rate": sum(
                sum(1 for rnd in r.rounds if rnd.buyer_mentioned_reputation)
                for r in run_list
            ) / sum(len(r.rounds) for r in run_list) if run_list else 0,
        }

    # Compute learning curves by condition
    for condition, run_list in by_condition.items():
        if run_list and run_list[0].rounds:
            num_rounds = len(run_list[0].rounds)
            curve = []
            for round_idx in range(num_rounds):
                avg_rep = sum(
                    r.rounds[round_idx].seller_reputation
                    for r in run_list if round_idx < len(r.rounds)
                ) / len(run_list)
                curve.append(avg_rep)
            learning_curves[condition] = curve

    return Study3Results(
        runs=runs,
        by_condition={k: aggregate(v) for k, v in by_condition.items()},
        by_model={k: aggregate(v) for k, v in by_model.items()},
        reputation_learning_curves=learning_curves,
    )


def _save_study3_results(results: Study3Results, output_dir: Path, run_id: str):
    """Save Study 3 results."""
    runs_path = output_dir / f"{run_id}_runs.jsonl"
    with open(runs_path, "w") as f:
        for run in results.runs:
            f.write(json.dumps({
                "run_id": run.run_id,
                "condition": run.condition,
                "model": run.model,
                "reputation_influence": run.reputation_influence_score,
                "learning_slope": run.learning_curve_slope,
                "num_rounds": len(run.rounds),
            }) + "\n")

    summary_path = output_dir / f"{run_id}_summary.json"
    with open(summary_path, "w") as f:
        json.dump({
            "total_runs": len(results.runs),
            "by_condition": results.by_condition,
            "by_model": results.by_model,
            "learning_curves": results.reputation_learning_curves,
        }, f, indent=2)

    print(f"\nResults saved to:")
    print(f"  {runs_path}")
    print(f"  {summary_path}")


def _print_study3_summary(results: Study3Results):
    """Print Study 3 summary."""
    print("\n" + "=" * 60)
    print("STUDY 3 RESULTS SUMMARY (Exploratory)")
    print("=" * 60)

    print("\nBy Condition:")
    for condition, metrics in results.by_condition.items():
        print(f"\n  {condition}:")
        print(f"    n = {metrics['n']}")
        print(f"    Reputation influence: {metrics['avg_reputation_influence']:.2f}")
        print(f"    Learning slope: {metrics['avg_learning_slope']:+.1f}")
        print(f"    Mentioned reputation: {metrics['mentioned_reputation_rate']:.1%}")

    print("\nBy Model:")
    for model, metrics in results.by_model.items():
        model_short = model[:30] + "..." if len(model) > 30 else model
        print(f"\n  {model_short}:")
        print(f"    Reputation influence: {metrics['avg_reputation_influence']:.2f}")
