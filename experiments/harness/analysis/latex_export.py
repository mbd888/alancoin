"""
LaTeX table export for publication.

Generates publication-ready tables in LaTeX format
for the research paper.
"""

from pathlib import Path
from typing import Optional


def export_to_latex(
    data: dict,
    output_path: Path,
    caption: str = "",
    label: str = "",
) -> str:
    """
    Export data to LaTeX table format.

    Args:
        data: Data dictionary to export
        output_path: Path to save .tex file
        caption: Table caption
        label: Table label for references

    Returns:
        LaTeX table string
    """
    if "by_condition" in data:
        latex = _create_condition_table(data, caption, label)
    elif "by_model" in data:
        latex = _create_model_table(data, caption, label)
    elif "pattern_frequencies" in data:
        latex = _create_pattern_table_latex(data, caption, label)
    else:
        latex = _create_generic_table(data, caption, label)

    # Save to file
    output_path = Path(output_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        f.write(latex)

    return latex


def _create_condition_table(
    data: dict,
    caption: str,
    label: str,
) -> str:
    """Create table for condition comparison results."""
    by_condition = data.get("by_condition", {})

    # Extract conditions and split into factors
    conditions = list(by_condition.keys())

    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{" + (caption or "Baseline Economic Behavior by Condition") + "}",
        r"\label{" + (label or "tab:conditions") + "}",
        r"\begin{tabular}{llccc}",
        r"\toprule",
        r"Competition & Constraint & Price Ratio & Overpay \% & $n$ \\",
        r"\midrule",
    ]

    for condition in sorted(conditions):
        metrics = by_condition[condition]
        parts = condition.split("_")
        competition = parts[0] if parts else condition
        constraint = parts[1] if len(parts) > 1 else "none"

        price_ratio = metrics.get("avg_price_ratio", 0)
        overpay = metrics.get("avg_overpayment_rate", 0) * 100
        n = metrics.get("n", 0)

        lines.append(
            f"{competition} & {constraint} & {price_ratio:.2f} & {overpay:.1f}\\% & {n} \\\\"
        )

    lines.extend([
        r"\bottomrule",
        r"\end{tabular}",
        r"\end{table}",
    ])

    return "\n".join(lines)


def _create_model_table(
    data: dict,
    caption: str,
    label: str,
) -> str:
    """Create table for model comparison results."""
    by_model = data.get("by_model", {})

    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{" + (caption or "Economic Behavior by Model") + "}",
        r"\label{" + (label or "tab:models") + "}",
        r"\begin{tabular}{lccc}",
        r"\toprule",
        r"Model & Price Ratio & Overpay \% & $n$ \\",
        r"\midrule",
    ]

    for model in sorted(by_model.keys()):
        metrics = by_model[model]

        # Shorten model name
        short_name = model.split("/")[-1]
        if len(short_name) > 25:
            short_name = short_name[:22] + "..."

        price_ratio = metrics.get("avg_price_ratio", 0)
        overpay = metrics.get("avg_overpayment_rate", 0) * 100
        n = metrics.get("n", 0)

        lines.append(
            f"{short_name} & {price_ratio:.2f} & {overpay:.1f}\\% & {n} \\\\"
        )

    lines.extend([
        r"\bottomrule",
        r"\end{tabular}",
        r"\end{table}",
    ])

    return "\n".join(lines)


def _create_pattern_table_latex(
    data: dict,
    caption: str,
    label: str,
) -> str:
    """Create table for reasoning pattern frequencies."""
    pattern_freq = data.get("pattern_frequencies", {})

    if not pattern_freq:
        return ""

    patterns = list(pattern_freq.keys())
    models = list(next(iter(pattern_freq.values())).keys())
    short_models = [m.split("/")[-1][:10] for m in models]

    # Header
    cols = "l" + "c" * len(models)
    header = "Pattern & " + " & ".join(short_models) + r" \\"

    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{" + (caption or "Reasoning Pattern Frequency by Model") + "}",
        r"\label{" + (label or "tab:patterns") + "}",
        r"\begin{tabular}{" + cols + "}",
        r"\toprule",
        header,
        r"\midrule",
    ]

    for pattern in patterns:
        row = pattern.replace("_", r"\_")
        for model in models:
            freq = pattern_freq[pattern].get(model, {}).get("frequency", 0)
            row += f" & {freq:.0%}"
        row += r" \\"
        lines.append(row)

    lines.extend([
        r"\bottomrule",
        r"\end{tabular}",
        r"\end{table}",
    ])

    return "\n".join(lines)


def _create_generic_table(
    data: dict,
    caption: str,
    label: str,
) -> str:
    """Create generic table from dict data."""
    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{" + (caption or "Results") + "}",
        r"\label{" + (label or "tab:results") + "}",
        r"\begin{tabular}{lc}",
        r"\toprule",
        r"Metric & Value \\",
        r"\midrule",
    ]

    for key, value in data.items():
        if isinstance(value, (int, float)):
            formatted = f"{value:.3f}" if isinstance(value, float) else str(value)
            key_escaped = key.replace("_", r"\_")
            lines.append(f"{key_escaped} & {formatted} \\\\")

    lines.extend([
        r"\bottomrule",
        r"\end{tabular}",
        r"\end{table}",
    ])

    return "\n".join(lines)


def create_pattern_table(
    coded_decisions: list,
    output_path: Path,
) -> str:
    """
    Create pattern frequency table from coded decisions.

    Args:
        coded_decisions: List of CodedDecision objects
        output_path: Path to save .tex file

    Returns:
        LaTeX table string
    """
    from .trace_analysis import ReasoningPattern

    # Count patterns by model
    by_model = {}
    for decision in coded_decisions:
        model = decision.model
        if model not in by_model:
            by_model[model] = {p: 0 for p in ReasoningPattern}
            by_model[model]["_total"] = 0

        by_model[model]["_total"] += 1
        for pattern in decision.patterns_detected:
            by_model[model][pattern] += 1

    # Convert to frequencies
    pattern_freq = {}
    for pattern in ReasoningPattern:
        pattern_freq[pattern.value] = {}
        for model, counts in by_model.items():
            total = counts.get("_total", 1)
            pattern_freq[pattern.value][model] = {
                "count": counts.get(pattern, 0),
                "frequency": counts.get(pattern, 0) / total,
            }

    return export_to_latex(
        {"pattern_frequencies": pattern_freq},
        output_path,
        caption="Reasoning Pattern Frequency by Model",
        label="tab:patterns",
    )


def create_failure_mode_table(
    failure_data: dict[str, dict[str, int]],
    output_path: Path,
) -> str:
    """
    Create failure mode comparison table.

    Args:
        failure_data: Dict mapping model to failure mode counts
        output_path: Path to save .tex file

    Returns:
        LaTeX table string
    """
    models = list(failure_data.keys())
    modes = set()
    for counts in failure_data.values():
        modes.update(counts.keys())
    modes = sorted(modes)

    # Compute totals for percentages
    totals = {m: sum(failure_data[m].values()) for m in models}

    # Header
    cols = "l" + "c" * len(models)
    short_models = [m.split("/")[-1][:10] for m in models]
    header = "Failure Mode & " + " & ".join(short_models) + r" \\"

    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{Failure Mode Distribution When Compromised}",
        r"\label{tab:failure-modes}",
        r"\begin{tabular}{" + cols + "}",
        r"\toprule",
        header,
        r"\midrule",
    ]

    for mode in modes:
        row = mode.replace("_", r"\_")
        for model in models:
            count = failure_data[model].get(mode, 0)
            total = totals.get(model, 1)
            pct = count / total * 100 if total > 0 else 0
            row += f" & {pct:.0f}\\%"
        row += r" \\"
        lines.append(row)

    lines.extend([
        r"\midrule",
    ])

    # Add totals row
    row = "Total compromised"
    for model in models:
        row += f" & {totals[model]}"
    row += r" \\"
    lines.append(row)

    lines.extend([
        r"\bottomrule",
        r"\end{tabular}",
        r"\end{table}",
    ])

    latex = "\n".join(lines)

    output_path = Path(output_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        f.write(latex)

    return latex


def create_anova_table(
    anova_results: dict,
    output_path: Path,
) -> str:
    """
    Create ANOVA results table.

    Args:
        anova_results: Dict with ANOVA results
        output_path: Path to save .tex file

    Returns:
        LaTeX table string
    """
    lines = [
        r"\begin{table}[htbp]",
        r"\centering",
        r"\caption{Two-Way ANOVA Results (Model $\times$ Condition)}",
        r"\label{tab:anova}",
        r"\begin{tabular}{lcccc}",
        r"\toprule",
        r"Source & $F$ & $p$ & $\eta^2$ & Significant \\",
        r"\midrule",
    ]

    for source, result in anova_results.items():
        if source.startswith("main_effect"):
            factor = source.replace("main_effect_", "").replace("_", r"\_")
            f_val = result.get("F", 0)
            p_val = result.get("p", 1)
            eta = result.get("eta_sq", 0)
            sig = "*" if result.get("significant", False) else ""

            p_str = f"{p_val:.3f}" if p_val >= 0.001 else "$<$.001"
            lines.append(
                f"{factor} & {f_val:.2f} & {p_str} & {eta:.3f} & {sig} \\\\"
            )

    lines.extend([
        r"\bottomrule",
        r"\multicolumn{5}{l}{\small * $p < .05$}",
        r"\end{tabular}",
        r"\end{table}",
    ])

    latex = "\n".join(lines)

    output_path = Path(output_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        f.write(latex)

    return latex
