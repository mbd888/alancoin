"""
Metrics computation for experiment analysis.

Provides standardized metrics for price efficiency, task completion,
and economic behavior analysis.
"""

from dataclasses import dataclass
from typing import Optional
import statistics


@dataclass
class PriceMetrics:
    """Price-related metrics."""

    # Efficiency metrics
    mean_price_ratio: float  # avg(paid / reference)
    median_price_ratio: float
    std_price_ratio: float

    # Distribution
    overpayment_rate: float  # % of purchases > 1.2× reference
    underpayment_rate: float  # % of purchases < 0.8× reference
    fair_price_rate: float  # % within 0.8-1.2× reference

    # Absolute values
    total_spent: float
    mean_transaction: float
    min_transaction: float
    max_transaction: float

    # Sample info
    n_transactions: int


@dataclass
class CompletionMetrics:
    """Task completion metrics."""

    completion_rate: float  # % of tasks completed
    attempted_rate: float  # % of tasks attempted
    success_rate: float  # % of attempts that succeeded

    budget_utilization: float  # spent / budget
    avg_rounds_to_complete: float

    n_tasks: int
    n_completed: int
    n_attempted: int


@dataclass
class NegotiationMetrics:
    """Negotiation behavior metrics."""

    negotiation_rate: float  # % of interactions involving negotiation
    avg_negotiation_rounds: float
    buyer_win_rate: float  # % where buyer got lower than list price

    avg_discount_obtained: float  # as fraction of list price
    first_offer_ratio: float  # avg first offer / list price

    n_negotiations: int


def compute_price_metrics(
    transactions: list[dict],
    reference_field: str = "reference_price",
    amount_field: str = "amount",
) -> PriceMetrics:
    """
    Compute price efficiency metrics from transaction data.

    Args:
        transactions: List of transaction dicts
        reference_field: Field name for reference price
        amount_field: Field name for transaction amount

    Returns:
        PriceMetrics with computed values
    """
    if not transactions:
        return PriceMetrics(
            mean_price_ratio=0, median_price_ratio=0, std_price_ratio=0,
            overpayment_rate=0, underpayment_rate=0, fair_price_rate=0,
            total_spent=0, mean_transaction=0, min_transaction=0, max_transaction=0,
            n_transactions=0,
        )

    # Compute price ratios
    ratios = []
    amounts = []

    for tx in transactions:
        amount = tx.get(amount_field, 0)
        reference = tx.get(reference_field, amount)  # Default to amount if no reference

        if reference > 0:
            ratios.append(amount / reference)
        amounts.append(amount)

    if not ratios:
        ratios = [1.0]  # Avoid empty list issues

    # Distribution metrics
    overpays = sum(1 for r in ratios if r > 1.2)
    underpays = sum(1 for r in ratios if r < 0.8)
    fair = sum(1 for r in ratios if 0.8 <= r <= 1.2)

    return PriceMetrics(
        mean_price_ratio=statistics.mean(ratios),
        median_price_ratio=statistics.median(ratios),
        std_price_ratio=statistics.stdev(ratios) if len(ratios) > 1 else 0,
        overpayment_rate=overpays / len(ratios),
        underpayment_rate=underpays / len(ratios),
        fair_price_rate=fair / len(ratios),
        total_spent=sum(amounts),
        mean_transaction=statistics.mean(amounts),
        min_transaction=min(amounts),
        max_transaction=max(amounts),
        n_transactions=len(transactions),
    )


def compute_completion_metrics(
    tasks: list[dict],
    budget: float = 10.0,
) -> CompletionMetrics:
    """
    Compute task completion metrics.

    Args:
        tasks: List of task dicts with 'completed', 'attempted', 'spent' fields
        budget: Total budget available

    Returns:
        CompletionMetrics with computed values
    """
    if not tasks:
        return CompletionMetrics(
            completion_rate=0, attempted_rate=0, success_rate=0,
            budget_utilization=0, avg_rounds_to_complete=0,
            n_tasks=0, n_completed=0, n_attempted=0,
        )

    n_tasks = len(tasks)
    n_completed = sum(1 for t in tasks if t.get("completed", False))
    n_attempted = sum(1 for t in tasks if t.get("attempted", False))
    total_spent = sum(t.get("spent", 0) for t in tasks)

    rounds_to_complete = [
        t.get("rounds", 1) for t in tasks if t.get("completed", False)
    ]

    return CompletionMetrics(
        completion_rate=n_completed / n_tasks if n_tasks > 0 else 0,
        attempted_rate=n_attempted / n_tasks if n_tasks > 0 else 0,
        success_rate=n_completed / n_attempted if n_attempted > 0 else 0,
        budget_utilization=total_spent / budget if budget > 0 else 0,
        avg_rounds_to_complete=(
            statistics.mean(rounds_to_complete) if rounds_to_complete else 0
        ),
        n_tasks=n_tasks,
        n_completed=n_completed,
        n_attempted=n_attempted,
    )


def compute_negotiation_metrics(
    negotiations: list[dict],
) -> NegotiationMetrics:
    """
    Compute negotiation behavior metrics.

    Args:
        negotiations: List of negotiation dicts

    Returns:
        NegotiationMetrics with computed values
    """
    if not negotiations:
        return NegotiationMetrics(
            negotiation_rate=0, avg_negotiation_rounds=0, buyer_win_rate=0,
            avg_discount_obtained=0, first_offer_ratio=0,
            n_negotiations=0,
        )

    rounds = [n.get("rounds", 1) for n in negotiations]
    discounts = []
    first_offers = []
    buyer_wins = 0

    for n in negotiations:
        list_price = n.get("listed_price", 0)
        final_price = n.get("final_price", list_price)
        first_offer = n.get("first_offer", final_price)

        if list_price > 0:
            discount = (list_price - final_price) / list_price
            discounts.append(discount)
            first_offers.append(first_offer / list_price)

            if final_price < list_price:
                buyer_wins += 1

    return NegotiationMetrics(
        negotiation_rate=len(negotiations),  # This should be normalized by caller
        avg_negotiation_rounds=statistics.mean(rounds) if rounds else 0,
        buyer_win_rate=buyer_wins / len(negotiations) if negotiations else 0,
        avg_discount_obtained=statistics.mean(discounts) if discounts else 0,
        first_offer_ratio=statistics.mean(first_offers) if first_offers else 0,
        n_negotiations=len(negotiations),
    )


def compute_exploitation_metrics(
    attacks: list[dict],
) -> dict:
    """
    Compute adversarial exploitation metrics.

    Args:
        attacks: List of attack result dicts

    Returns:
        Dict with exploitation metrics
    """
    if not attacks:
        return {
            "exploitation_rate": 0,
            "cba_block_rate": 0,
            "detection_rate": 0,
            "avg_damage": 0,
            "total_damage": 0,
            "n_attacks": 0,
        }

    exploited = sum(1 for a in attacks if a.get("exploited", False))
    cba_blocked = sum(1 for a in attacks if a.get("cba_blocked", False))
    detected = sum(1 for a in attacks if a.get("detected", False))
    damages = [a.get("damage", 0) for a in attacks if a.get("exploited", False)]

    return {
        "exploitation_rate": exploited / len(attacks),
        "cba_block_rate": cba_blocked / len(attacks),
        "detection_rate": detected / len(attacks),
        "avg_damage": statistics.mean(damages) if damages else 0,
        "total_damage": sum(damages),
        "n_attacks": len(attacks),
    }


def compute_model_comparison(
    results_by_model: dict[str, list[dict]],
) -> dict:
    """
    Compute comparative metrics across models.

    Args:
        results_by_model: Dict mapping model names to result lists

    Returns:
        Comparative analysis dict
    """
    comparison = {}

    for model, results in results_by_model.items():
        if not results:
            continue

        # Aggregate relevant metrics
        price_ratios = [r.get("price_ratio", 1.0) for r in results if "price_ratio" in r]
        exploited = [r.get("exploited", False) for r in results if "exploited" in r]

        comparison[model] = {
            "n_results": len(results),
            "avg_price_ratio": statistics.mean(price_ratios) if price_ratios else 0,
            "exploitation_rate": sum(exploited) / len(exploited) if exploited else 0,
        }

    return comparison
