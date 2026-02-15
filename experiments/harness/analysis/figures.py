"""
Figure generation for publication.

Creates matplotlib visualizations for the research paper,
including bar charts, dose-response plots, heatmaps, and Sankey diagrams.
"""

from pathlib import Path
from typing import Optional
import numpy as np

try:
    import matplotlib.pyplot as plt
    import matplotlib.patches as mpatches
    HAS_MATPLOTLIB = True
except ImportError:
    HAS_MATPLOTLIB = False


def create_figure_set(
    results: dict,
    output_dir: Path,
    style: str = "paper",
) -> dict[str, Path]:
    """
    Create all figures for the paper.

    Args:
        results: Experiment results
        output_dir: Directory to save figures
        style: Figure style ("paper" or "presentation")

    Returns:
        Dict mapping figure name to file path
    """
    if not HAS_MATPLOTLIB:
        print("Warning: matplotlib not available, skipping figure generation")
        return {}

    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    # Set style
    if style == "paper":
        plt.rcParams.update({
            "font.size": 10,
            "axes.titlesize": 11,
            "axes.labelsize": 10,
            "xtick.labelsize": 9,
            "ytick.labelsize": 9,
            "legend.fontsize": 9,
            "figure.dpi": 150,
            "savefig.dpi": 300,
        })

    figures = {}

    # Generate figures based on available data
    if "by_condition" in results:
        fig_path = _create_condition_comparison(results, output_dir)
        figures["condition_comparison"] = fig_path

    if "by_model" in results:
        fig_path = _create_model_comparison(results, output_dir)
        figures["model_comparison"] = fig_path

    if "dose_response" in results:
        fig_path = _create_dose_response_plot(results, output_dir)
        figures["dose_response"] = fig_path

    if "pattern_frequencies" in results:
        fig_path = _create_pattern_heatmap(results, output_dir)
        figures["pattern_heatmap"] = fig_path

    return figures


def _create_condition_comparison(
    results: dict,
    output_dir: Path,
) -> Path:
    """Create bar chart comparing conditions."""
    by_condition = results.get("by_condition", {})

    conditions = list(by_condition.keys())
    values = [by_condition[c].get("avg_price_ratio", 0) for c in conditions]
    errors = [by_condition[c].get("std_price_ratio", 0) for c in conditions]

    fig, ax = plt.subplots(figsize=(8, 5))

    x = np.arange(len(conditions))
    bars = ax.bar(x, values, yerr=errors, capsize=3, color="steelblue", alpha=0.8)

    ax.set_ylabel("Average Price Ratio")
    ax.set_xlabel("Condition")
    ax.set_title("Price Efficiency by Condition")
    ax.set_xticks(x)
    ax.set_xticklabels([c.replace("_", "\n") for c in conditions], rotation=0)

    # Add horizontal line at y=1 (fair price)
    ax.axhline(y=1.0, color="red", linestyle="--", alpha=0.5, label="Reference price")
    ax.legend()

    plt.tight_layout()

    path = output_dir / "condition_comparison.png"
    plt.savefig(path)
    plt.close()

    return path


def _create_model_comparison(
    results: dict,
    output_dir: Path,
) -> Path:
    """Create grouped bar chart comparing models."""
    by_model = results.get("by_model", {})

    models = list(by_model.keys())
    # Shorten model names for display
    short_names = [m.split("/")[-1][:15] for m in models]

    metrics = ["avg_price_ratio", "avg_overpayment_rate"]
    metric_labels = ["Price Ratio", "Overpay Rate"]

    x = np.arange(len(models))
    width = 0.35

    fig, ax = plt.subplots(figsize=(10, 5))

    for i, (metric, label) in enumerate(zip(metrics, metric_labels)):
        values = [by_model[m].get(metric, 0) for m in models]
        offset = width * (i - 0.5)
        ax.bar(x + offset, values, width, label=label, alpha=0.8)

    ax.set_ylabel("Value")
    ax.set_xlabel("Model")
    ax.set_title("Model Comparison")
    ax.set_xticks(x)
    ax.set_xticklabels(short_names, rotation=45, ha="right")
    ax.legend()

    plt.tight_layout()

    path = output_dir / "model_comparison.png"
    plt.savefig(path)
    plt.close()

    return path


def _create_dose_response_plot(
    results: dict,
    output_dir: Path,
) -> Path:
    """Create dose-response curves."""
    dose_response = results.get("dose_response", {})

    fig, ax = plt.subplots(figsize=(8, 5))

    colors = plt.cm.Set1(np.linspace(0, 1, len(dose_response)))

    for (adversary, data), color in zip(dose_response.items(), colors):
        if isinstance(data, list):
            # data is list of (intensity, rate) tuples
            doses = [d[0] for d in data]
            rates = [d[1] for d in data]
        else:
            # data is DoseResponseCurve object
            doses = data.doses if hasattr(data, "doses") else []
            rates = data.responses if hasattr(data, "responses") else []

        if doses and rates:
            ax.plot(doses, rates, "o-", label=adversary, color=color, alpha=0.8)

            # Add ED50 vertical line if available
            if hasattr(data, "ed50"):
                ax.axvline(x=data.ed50, color=color, linestyle=":", alpha=0.5)

    ax.set_xlabel("Adversary Intensity")
    ax.set_ylabel("Exploitation Rate")
    ax.set_title("Dose-Response Curves by Adversary Type")
    ax.legend()
    ax.set_ylim(0, 1)

    plt.tight_layout()

    path = output_dir / "dose_response.png"
    plt.savefig(path)
    plt.close()

    return path


def _create_pattern_heatmap(
    results: dict,
    output_dir: Path,
) -> Path:
    """Create heatmap of pattern frequencies by model."""
    pattern_freq = results.get("pattern_frequencies", {})

    if not pattern_freq:
        # Create empty figure
        fig, ax = plt.subplots(figsize=(8, 6))
        ax.text(0.5, 0.5, "No pattern data available", ha="center", va="center")
        path = output_dir / "pattern_heatmap.png"
        plt.savefig(path)
        plt.close()
        return path

    patterns = list(pattern_freq.keys())
    models = list(next(iter(pattern_freq.values())).keys())

    # Build matrix
    matrix = np.zeros((len(patterns), len(models)))
    for i, pattern in enumerate(patterns):
        for j, model in enumerate(models):
            freq = pattern_freq[pattern].get(model, {}).get("frequency", 0)
            matrix[i, j] = freq

    fig, ax = plt.subplots(figsize=(10, 8))

    im = ax.imshow(matrix, cmap="YlOrRd", aspect="auto")

    # Labels
    ax.set_xticks(np.arange(len(models)))
    ax.set_yticks(np.arange(len(patterns)))

    short_models = [m.split("/")[-1][:12] for m in models]
    ax.set_xticklabels(short_models, rotation=45, ha="right")
    ax.set_yticklabels(patterns)

    ax.set_xlabel("Model")
    ax.set_ylabel("Reasoning Pattern")
    ax.set_title("Reasoning Pattern Frequency by Model")

    # Colorbar
    cbar = plt.colorbar(im)
    cbar.set_label("Frequency")

    # Add text annotations
    for i in range(len(patterns)):
        for j in range(len(models)):
            text = ax.text(j, i, f"{matrix[i, j]:.0%}",
                           ha="center", va="center", color="black", fontsize=8)

    plt.tight_layout()

    path = output_dir / "pattern_heatmap.png"
    plt.savefig(path)
    plt.close()

    return path


def create_sankey_diagram(
    pattern_to_outcome: dict[str, dict[str, int]],
    output_path: Path,
) -> Path:
    """
    Create Sankey diagram showing pattern → outcome flows.

    Args:
        pattern_to_outcome: Dict mapping patterns to outcome counts
        output_path: Path to save figure

    Returns:
        Path to saved figure
    """
    if not HAS_MATPLOTLIB:
        return output_path

    # Note: matplotlib doesn't have native Sankey support,
    # using a simplified bar visualization instead

    fig, ax = plt.subplots(figsize=(12, 6))

    patterns = list(pattern_to_outcome.keys())
    outcomes = set()
    for counts in pattern_to_outcome.values():
        outcomes.update(counts.keys())
    outcomes = list(outcomes)

    x = np.arange(len(patterns))
    width = 0.8 / len(outcomes)

    for i, outcome in enumerate(outcomes):
        counts = [pattern_to_outcome[p].get(outcome, 0) for p in patterns]
        offset = width * (i - len(outcomes) / 2 + 0.5)
        ax.bar(x + offset, counts, width, label=outcome, alpha=0.8)

    ax.set_xlabel("Reasoning Pattern")
    ax.set_ylabel("Count")
    ax.set_title("Reasoning Pattern → Decision Outcome")
    ax.set_xticks(x)
    ax.set_xticklabels(patterns, rotation=45, ha="right")
    ax.legend(title="Outcome")

    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()

    return output_path


def create_failure_mode_chart(
    failure_modes: dict[str, dict[str, int]],
    output_path: Path,
) -> Path:
    """
    Create chart showing failure mode distribution by model.

    Args:
        failure_modes: Dict mapping model to failure mode counts
        output_path: Path to save figure

    Returns:
        Path to saved figure
    """
    if not HAS_MATPLOTLIB:
        return output_path

    models = list(failure_modes.keys())
    modes = set()
    for counts in failure_modes.values():
        modes.update(counts.keys())
    modes = sorted(modes)

    # Stacked bar chart
    fig, ax = plt.subplots(figsize=(10, 6))

    x = np.arange(len(models))
    bottom = np.zeros(len(models))

    colors = plt.cm.Set2(np.linspace(0, 1, len(modes)))

    for mode, color in zip(modes, colors):
        values = [failure_modes[m].get(mode, 0) for m in models]
        ax.bar(x, values, bottom=bottom, label=mode, color=color, alpha=0.8)
        bottom += values

    ax.set_xlabel("Model")
    ax.set_ylabel("Count")
    ax.set_title("Failure Mode Distribution by Model")

    short_models = [m.split("/")[-1][:15] for m in models]
    ax.set_xticks(x)
    ax.set_xticklabels(short_models, rotation=45, ha="right")
    ax.legend(title="Failure Mode")

    plt.tight_layout()
    plt.savefig(output_path)
    plt.close()

    return output_path
