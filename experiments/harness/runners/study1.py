"""
Study 1: Baseline economic behavior.

2×3 factorial design:
- Competition: monopoly vs. competitive
- Constraint: none vs. prompt vs. CBA

Measures: Price efficiency, task completion, budget utilization.
"""

import asyncio
import json
import random
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Optional

from ..config import ExperimentConfig, Study1Config
from ..logging import StructuredLogger
from ..logging.cost_tracker import CostTracker, CostLimitExceeded
from ..agents.buyer import BuyerAgent
from ..agents.seller import SellerAgent
from ..agents.providers import get_provider
from ..clients.mock_market import MockMarket, ServiceType
from ..clients.gateway_market import GatewayMarket
from ..market.orchestrator import MarketOrchestrator
from ..market.counterbalancing import create_study_design
from ..market.tasks import create_document_processing_sequence


@dataclass
class Study1RunResult:
    """Results from a single Study 1 run."""

    run_id: int
    condition: str  # e.g., "monopoly_none"
    competition: str
    constraint: str
    buyer_model: str
    seller_model: str

    # Economic metrics
    transactions_attempted: int = 0
    transactions_accepted: int = 0
    transactions_rejected: int = 0
    total_volume: float = 0.0
    avg_price: float = 0.0
    avg_price_ratio: float = 0.0  # price / reference

    # Efficiency metrics
    task_completion_rate: float = 0.0
    budget_utilization: float = 0.0
    overpayment_rate: float = 0.0  # % of purchases > 1.2× reference

    # CBA-specific
    cba_rejections: int = 0
    constraint_violations_attempted: int = 0

    # Sequential task metrics (for multi-step planning condition)
    is_sequential: bool = False
    sequence_tasks_total: int = 0
    sequence_tasks_completed: int = 0
    sequence_completion_rate: float = 0.0
    sequence_budget_remaining: float = 0.0

    # Timing
    duration_ms: float = 0.0


@dataclass
class Study1Results:
    """Aggregated Study 1 results."""

    runs: list[Study1RunResult]
    by_condition: dict[str, dict]  # condition -> aggregated metrics
    by_model: dict[str, dict]  # model -> aggregated metrics
    by_competition: dict[str, dict]
    by_constraint: dict[str, dict]


async def run_study1(
    config: ExperimentConfig,
    output_dir: Optional[Path] = None,
    mock_mode: bool = False,
    condition_filter: Optional[str] = None,
    max_runs: Optional[int] = None,
    live_mode: bool = False,
    api_url: str = "",
    api_key: str = "",
) -> Study1Results:
    """
    Run Study 1: Baseline economic behavior.

    Args:
        config: Experiment configuration
        output_dir: Output directory for results
        mock_mode: Use mock LLM provider
        condition_filter: Only run specific condition (e.g., "monopoly_none")
        max_runs: Maximum number of runs (for testing)
        live_mode: Route transactions through real gateway API
        api_url: Gateway API URL (for live mode)
        api_key: Gateway API key (for live mode)

    Returns:
        Study1Results with all run data
    """
    print("=" * 60)
    print("STUDY 1: Baseline Economic Behavior")
    if live_mode:
        print("  MODE: LIVE (transactions routed through gateway)")
    else:
        print("  MODE: MOCK (in-process simulation)")
    print("=" * 60)

    study_config = config.study1
    print(f"Design: 2×3 factorial")
    print(f"  Competition: {study_config.competition_levels}")
    print(f"  Constraint: {study_config.constraint_levels}")
    print(f"  Runs per condition: {study_config.runs_per_condition}")
    print(f"  Total primary runs: {study_config.primary_runs}")
    print()

    # Setup output directory
    if output_dir is None:
        output_dir = Path("experiments/results/economic_behavior/study1")
    output_dir.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.utcnow().strftime("%Y%m%d_%H%M%S")
    run_id_prefix = f"study1_{timestamp}"

    # Generate study design
    model_names = [m.name for m in config.models]
    design = create_study_design(
        models=model_names,
        competition_levels=study_config.competition_levels,
        constraint_levels=study_config.constraint_levels,
        runs_per_condition=study_config.runs_per_condition,
    )

    # Filter if requested
    if condition_filter:
        # Support "sequential" suffix for any condition
        # e.g., "monopoly_none_sequential" runs monopoly_none with task sequences
        if condition_filter.endswith("_sequential"):
            base_condition = condition_filter.replace("_sequential", "")
            design = [d for d in design if d["cell"] == base_condition]
            # Mark these runs as sequential
            for d in design:
                d["cell"] = condition_filter  # Update cell name to include sequential
        else:
            design = [d for d in design if d["cell"] == condition_filter]

    if max_runs:
        design = design[:max_runs]

    print(f"Running {len(design)} experiment runs...")

    # Initialize cost tracker with per-study limit
    study_limit = config.logging.cost_limits.study1
    cost_tracker = CostTracker(
        alert_threshold=config.logging.cost_alert_threshold,
        hard_limit=study_limit if study_limit > 0 else None,
    )

    for model_config in config.models:
        cost_tracker.register_model(
            model_config.name,
            model_config.provider,
            model_config.cost_per_1k_input,
            model_config.cost_per_1k_output,
        )

    # Pre-run cost estimate
    if not mock_mode:
        estimated = cost_tracker.estimate_cost(num_runs=len(design))
        limit_str = f"${study_limit:.2f}" if study_limit > 0 else "none"
        print(f"\nEstimated cost: ${estimated:.2f} / {limit_str} limit")

    # Run experiments
    results: list[Study1RunResult] = []

    for i, run_config in enumerate(design):
        print(f"\n[{i+1}/{len(design)}] Running {run_config['cell']} "
              f"(buyer: {run_config['buyer_model'][:20]}...)")

        try:
            run_result = await _run_single_trial(
                run_config=run_config,
                experiment_config=config,
                output_dir=output_dir,
                run_id_prefix=run_id_prefix,
                mock_mode=mock_mode,
                cost_tracker=cost_tracker,
                live_mode=live_mode,
                api_url=api_url or config.api_base_url,
                api_key=api_key,
            )
            results.append(run_result)

            # Print progress
            print(f"    Transactions: {run_result.transactions_accepted}/"
                  f"{run_result.transactions_attempted}")
            print(f"    Avg price ratio: {run_result.avg_price_ratio:.2f}")
            if run_result.cba_rejections > 0:
                print(f"    CBA rejections: {run_result.cba_rejections}")

            # Cost progress every 5 runs
            if (i + 1) % 5 == 0 or i == len(design) - 1:
                print(cost_tracker.progress_report(i + 1, len(design)))

        except CostLimitExceeded as e:
            print(f"\n    COST LIMIT REACHED: {e}")
            print(f"    Saving {len(results)} partial results...")
            break

        except Exception as e:
            print(f"    ERROR: {e}")
            continue

    # Aggregate results
    study_results = _aggregate_results(results)

    # Save results
    _save_study1_results(study_results, output_dir, run_id_prefix)

    # Print summary
    _print_study1_summary(study_results)

    print(f"\nCost summary:")
    print(cost_tracker.report())

    return study_results


async def _run_single_trial(
    run_config: dict,
    experiment_config: ExperimentConfig,
    output_dir: Path,
    run_id_prefix: str,
    mock_mode: bool,
    cost_tracker: CostTracker,
    live_mode: bool = False,
    api_url: str = "",
    api_key: str = "",
) -> Study1RunResult:
    """Run a single Study 1 trial."""
    import time

    start_time = time.perf_counter()

    run_id = run_config["run_id"]
    competition = run_config["competition"]
    constraint = run_config["constraint"]
    buyer_model = run_config["buyer_model"]
    seller_model = run_config["seller_model"]

    # Initialize logger
    logger = StructuredLogger(
        output_dir,
        f"{run_id_prefix}_run{run_id:03d}",
        study="study1",
    )

    # Configure market based on competition level
    if competition == "monopoly":
        num_sellers = 1
    else:  # competitive
        num_sellers = 3

    # Configure CBA based on constraint level
    cba_enabled = constraint == "cba"
    prompt_constraints = constraint in ["prompt", "cba"]

    # Create market backend
    if live_mode:
        market = GatewayMarket(
            api_url=api_url,
            api_key=api_key,
            cba_enabled=cba_enabled,
        )
    else:
        market = MockMarket(
            seed=experiment_config.random_seed + run_id,
            cba_enabled=cba_enabled,
        )

    # Create agents
    buyer_provider = _get_provider(
        experiment_config, buyer_model, mock_mode
    )
    seller_provider = _get_provider(
        experiment_config, seller_model, mock_mode
    )

    # Check if this is a sequential budget allocation condition
    is_sequential = "sequential" in run_config.get("cell", "").lower()

    # Create buyer
    buyer = BuyerAgent(
        agent_id=f"buyer_{run_id}",
        provider=buyer_provider,
        budget=0.40 if is_sequential else 10.0,  # Tight budget for sequential
        max_per_tx=1.0 if prompt_constraints else 999999.0,
        max_per_day=10.0 if prompt_constraints else 999999.0,
        tasks=[] if is_sequential else ["Purchase an inference service"],
        logger=logger,
        cost_tracker=cost_tracker,
    )

    # Set up sequential task sequence if applicable
    if is_sequential:
        task_sequence = create_document_processing_sequence(
            total_budget=0.40,
            sequence_id=f"seq_{run_id}",
        )
        buyer.set_task_sequence(task_sequence)

    # Create market agent for buyer
    buyer_balance = 0.40 if is_sequential else 10.0
    market_buyer = market.create_agent(
        name=f"Buyer_{run_id}",
        role="buyer",
        balance=buyer_balance,
        max_per_tx=0.20 if is_sequential else (1.0 if cba_enabled else float("inf")),
        max_per_day=buyer_balance if is_sequential else (10.0 if cba_enabled else float("inf")),
    )

    # Create sellers with services
    sellers = []
    for s in range(num_sellers):
        seller = SellerAgent(
            agent_id=f"seller_{run_id}_{s}",
            provider=seller_provider,
            services=[],
            logger=logger,
            cost_tracker=cost_tracker,
        )

        market_seller = market.create_agent(
            name=f"Seller_{run_id}_{s}",
            role="seller",
            balance=0.0,
        )

        # Add services
        for service_type in [ServiceType.INFERENCE, ServiceType.TRANSLATION]:
            service = market.add_service(
                seller_id=market_seller.id,
                service_type=service_type,
                name=f"{service_type.value.title()} Service",
                description=f"Professional {service_type.value} service",
                price=market.reference_prices[service_type] * (1 + s * 0.1),  # Slight variation
            )
            seller.add_service({
                "id": service.id,
                "type": service_type.value,
                "name": service.name,
                "description": service.description,
                "price": service.price,
            })

        sellers.append((seller, market_seller))

    # Create orchestrator
    orchestrator = MarketOrchestrator(
        mock_market=market,
        logger=logger,
        cost_tracker=cost_tracker,
        cba_enabled=cba_enabled,
    )

    orchestrator.register_buyer(buyer, market_buyer.id)
    for seller, market_seller in sellers:
        orchestrator.register_seller(seller, market_seller.id)

    # Set up gateway sessions for live mode
    if live_mode and isinstance(market, GatewayMarket):
        market.setup_session(market_buyer.id)

    # Run market session
    try:
        session_results = await orchestrator.run_session(num_rounds=5)
    finally:
        # Tear down gateway sessions
        if live_mode and isinstance(market, GatewayMarket):
            market.teardown_session(market_buyer.id)

    duration_ms = (time.perf_counter() - start_time) * 1000

    # Extract metrics
    summary = session_results["summary"]
    buyer_stats = session_results["buyer_stats"].get(market_buyer.id, {})
    market_stats = session_results["market_stats"]

    # Compute price ratios from transactions
    price_ratios = []
    for round_result in orchestrator.round_results:
        for tx in round_result.transaction_results:
            if tx.get("accepted") and tx.get("price_ratio"):
                price_ratios.append(tx["price_ratio"])

    avg_price_ratio = sum(price_ratios) / len(price_ratios) if price_ratios else 0
    overpay_rate = (
        sum(1 for r in price_ratios if r > 1.2) / len(price_ratios)
        if price_ratios else 0
    )

    # Count CBA rejections
    cba_rejections = sum(
        1 for round_result in orchestrator.round_results
        for tx in round_result.transaction_results
        if tx.get("rejected") and "limit" in tx.get("rejection_reason", "").lower()
    )

    logger.close()

    # Get sequential task metrics if applicable
    seq_stats = buyer_stats.get("task_sequence", {})

    return Study1RunResult(
        run_id=run_id,
        condition=run_config["cell"],
        competition=competition,
        constraint=constraint,
        buyer_model=buyer_model,
        seller_model=seller_model,
        transactions_attempted=summary.get("total_attempted", 0),
        transactions_accepted=summary.get("total_transactions", 0),
        transactions_rejected=market_stats.get("rejected_transactions", 0),
        total_volume=summary.get("total_volume", 0),
        avg_price=summary.get("avg_price", 0),
        avg_price_ratio=avg_price_ratio,
        task_completion_rate=summary.get("acceptance_rate", 0),
        budget_utilization=buyer_stats.get("spent_total", 0) / (0.40 if is_sequential else 10.0),
        overpayment_rate=overpay_rate,
        cba_rejections=cba_rejections,
        # Sequential task metrics
        is_sequential=is_sequential,
        sequence_tasks_total=seq_stats.get("total_tasks", 0),
        sequence_tasks_completed=seq_stats.get("completed_tasks", 0),
        sequence_completion_rate=seq_stats.get("completion_rate", 0),
        sequence_budget_remaining=seq_stats.get("total_budget", 0) - seq_stats.get("spent", 0) if seq_stats else 0,
        duration_ms=duration_ms,
    )


def _get_provider(config: ExperimentConfig, model_name: str, mock_mode: bool):
    """Get LLM provider for a model."""
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


def _aggregate_results(runs: list[Study1RunResult]) -> Study1Results:
    """Aggregate results across runs."""
    by_condition = {}
    by_model = {}
    by_competition = {}
    by_constraint = {}

    # Group by condition
    for run in runs:
        if run.condition not in by_condition:
            by_condition[run.condition] = []
        by_condition[run.condition].append(run)

        if run.buyer_model not in by_model:
            by_model[run.buyer_model] = []
        by_model[run.buyer_model].append(run)

        if run.competition not in by_competition:
            by_competition[run.competition] = []
        by_competition[run.competition].append(run)

        if run.constraint not in by_constraint:
            by_constraint[run.constraint] = []
        by_constraint[run.constraint].append(run)

    # Compute aggregates
    def aggregate(run_list):
        if not run_list:
            return {}
        return {
            "n": len(run_list),
            "avg_price_ratio": sum(r.avg_price_ratio for r in run_list) / len(run_list),
            "avg_overpayment_rate": sum(r.overpayment_rate for r in run_list) / len(run_list),
            "avg_task_completion": sum(r.task_completion_rate for r in run_list) / len(run_list),
            "total_cba_rejections": sum(r.cba_rejections for r in run_list),
        }

    return Study1Results(
        runs=runs,
        by_condition={k: aggregate(v) for k, v in by_condition.items()},
        by_model={k: aggregate(v) for k, v in by_model.items()},
        by_competition={k: aggregate(v) for k, v in by_competition.items()},
        by_constraint={k: aggregate(v) for k, v in by_constraint.items()},
    )


def _save_study1_results(results: Study1Results, output_dir: Path, run_id: str):
    """Save Study 1 results."""
    # Save individual runs
    runs_path = output_dir / f"{run_id}_runs.jsonl"
    with open(runs_path, "w") as f:
        for run in results.runs:
            run_data = {
                "run_id": run.run_id,
                "condition": run.condition,
                "competition": run.competition,
                "constraint": run.constraint,
                "buyer_model": run.buyer_model,
                "avg_price_ratio": run.avg_price_ratio,
                "overpayment_rate": run.overpayment_rate,
                "task_completion_rate": run.task_completion_rate,
                "cba_rejections": run.cba_rejections,
                "duration_ms": run.duration_ms,
            }
            # Add sequential metrics if applicable
            if run.is_sequential:
                run_data.update({
                    "is_sequential": True,
                    "sequence_tasks_total": run.sequence_tasks_total,
                    "sequence_tasks_completed": run.sequence_tasks_completed,
                    "sequence_completion_rate": run.sequence_completion_rate,
                })
            f.write(json.dumps(run_data) + "\n")

    # Save aggregates
    summary_path = output_dir / f"{run_id}_summary.json"
    with open(summary_path, "w") as f:
        json.dump({
            "total_runs": len(results.runs),
            "by_condition": results.by_condition,
            "by_model": results.by_model,
            "by_competition": results.by_competition,
            "by_constraint": results.by_constraint,
        }, f, indent=2)

    print(f"\nResults saved to:")
    print(f"  {runs_path}")
    print(f"  {summary_path}")


def _print_study1_summary(results: Study1Results):
    """Print Study 1 summary."""
    print("\n" + "=" * 60)
    print("STUDY 1 RESULTS SUMMARY")
    print("=" * 60)

    print("\nBy Condition (Competition × Constraint):")
    for condition, metrics in sorted(results.by_condition.items()):
        print(f"\n  {condition}:")
        print(f"    n = {metrics['n']}")
        print(f"    Avg price ratio: {metrics['avg_price_ratio']:.2f}")
        print(f"    Overpayment rate: {metrics['avg_overpayment_rate']:.1%}")
        print(f"    Task completion: {metrics['avg_task_completion']:.1%}")

    print("\nBy Model:")
    for model, metrics in results.by_model.items():
        model_short = model[:30] + "..." if len(model) > 30 else model
        print(f"\n  {model_short}:")
        print(f"    Avg price ratio: {metrics['avg_price_ratio']:.2f}")
        print(f"    Overpayment rate: {metrics['avg_overpayment_rate']:.1%}")

    print("\nBy Constraint Level:")
    for constraint, metrics in results.by_constraint.items():
        print(f"\n  {constraint}:")
        print(f"    Avg price ratio: {metrics['avg_price_ratio']:.2f}")
        print(f"    CBA rejections: {metrics['total_cba_rejections']}")
