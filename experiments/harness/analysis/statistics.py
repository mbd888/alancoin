"""
Statistical analysis for experiment results.

Provides ANOVA, Tukey HSD, effect sizes, and other statistical tests
needed for the research paper.
"""

import statistics
from dataclasses import dataclass
from typing import Optional
import numpy as np
from scipy import stats as scipy_stats


@dataclass
class ANOVAResult:
    """Result of ANOVA analysis."""

    f_statistic: float
    p_value: float
    df_between: int
    df_within: int
    effect_size_eta_sq: float
    significant: bool  # at p < 0.05


@dataclass
class TukeyResult:
    """Result of Tukey HSD post-hoc test."""

    comparisons: list[dict]  # List of pairwise comparisons
    significant_pairs: list[tuple[str, str]]


@dataclass
class EffectSize:
    """Effect size measurements."""

    cohens_d: float
    hedges_g: float
    interpretation: str  # "small", "medium", "large"


def run_anova(
    groups: dict[str, list[float]],
) -> ANOVAResult:
    """
    Run one-way ANOVA on grouped data.

    Args:
        groups: Dict mapping group names to lists of values

    Returns:
        ANOVAResult with test statistics
    """
    if len(groups) < 2:
        return ANOVAResult(
            f_statistic=0, p_value=1.0, df_between=0, df_within=0,
            effect_size_eta_sq=0, significant=False,
        )

    # Prepare data for scipy
    group_values = list(groups.values())

    # Filter out empty groups
    group_values = [g for g in group_values if len(g) > 0]

    if len(group_values) < 2:
        return ANOVAResult(
            f_statistic=0, p_value=1.0, df_between=0, df_within=0,
            effect_size_eta_sq=0, significant=False,
        )

    try:
        f_stat, p_value = scipy_stats.f_oneway(*group_values)
    except Exception:
        return ANOVAResult(
            f_statistic=0, p_value=1.0, df_between=0, df_within=0,
            effect_size_eta_sq=0, significant=False,
        )

    # Compute degrees of freedom
    k = len(group_values)  # Number of groups
    n_total = sum(len(g) for g in group_values)
    df_between = k - 1
    df_within = n_total - k

    # Compute effect size (eta squared)
    # eta_sq = SS_between / SS_total
    grand_mean = np.mean([v for g in group_values for v in g])
    ss_between = sum(
        len(g) * (np.mean(g) - grand_mean) ** 2
        for g in group_values
    )
    ss_total = sum(
        (v - grand_mean) ** 2
        for g in group_values for v in g
    )
    eta_sq = ss_between / ss_total if ss_total > 0 else 0

    return ANOVAResult(
        f_statistic=float(f_stat) if not np.isnan(f_stat) else 0,
        p_value=float(p_value) if not np.isnan(p_value) else 1.0,
        df_between=df_between,
        df_within=df_within,
        effect_size_eta_sq=eta_sq,
        significant=p_value < 0.05,
    )


def run_two_way_anova(
    data: list[dict],
    factor1: str,
    factor2: str,
    dependent: str,
) -> dict:
    """
    Run two-way ANOVA (e.g., model × condition).

    Args:
        data: List of observation dicts
        factor1: Name of first factor
        factor2: Name of second factor
        dependent: Name of dependent variable

    Returns:
        Dict with main effects and interaction
    """
    # Group data by factors
    groups_f1 = {}
    groups_f2 = {}
    interaction_groups = {}

    for obs in data:
        f1 = obs.get(factor1)
        f2 = obs.get(factor2)
        val = obs.get(dependent)

        if f1 is None or f2 is None or val is None:
            continue

        if f1 not in groups_f1:
            groups_f1[f1] = []
        groups_f1[f1].append(val)

        if f2 not in groups_f2:
            groups_f2[f2] = []
        groups_f2[f2].append(val)

        key = (f1, f2)
        if key not in interaction_groups:
            interaction_groups[key] = []
        interaction_groups[key].append(val)

    # Main effects
    main_effect_1 = run_anova(groups_f1)
    main_effect_2 = run_anova(groups_f2)

    # Interaction (simplified - uses cell means)
    # For proper interaction test, would need full factorial design
    cell_means = {k: np.mean(v) for k, v in interaction_groups.items()}

    return {
        "main_effect_" + factor1: {
            "F": main_effect_1.f_statistic,
            "p": main_effect_1.p_value,
            "eta_sq": main_effect_1.effect_size_eta_sq,
            "significant": main_effect_1.significant,
        },
        "main_effect_" + factor2: {
            "F": main_effect_2.f_statistic,
            "p": main_effect_2.p_value,
            "eta_sq": main_effect_2.effect_size_eta_sq,
            "significant": main_effect_2.significant,
        },
        "cell_means": {f"{k[0]}_{k[1]}": v for k, v in cell_means.items()},
    }


def tukey_hsd(
    groups: dict[str, list[float]],
) -> TukeyResult:
    """
    Run Tukey HSD post-hoc test for pairwise comparisons.

    Args:
        groups: Dict mapping group names to lists of values

    Returns:
        TukeyResult with all pairwise comparisons
    """
    group_names = list(groups.keys())
    comparisons = []
    significant_pairs = []

    # Compute pooled standard error
    all_values = [v for g in groups.values() for v in g]
    n_total = len(all_values)
    k = len(groups)

    if n_total <= k:
        return TukeyResult(comparisons=[], significant_pairs=[])

    # Mean squared error (within groups)
    grand_mean = np.mean(all_values)
    ss_within = sum(
        (v - np.mean(g)) ** 2
        for name, g in groups.items() for v in g
    )
    ms_within = ss_within / (n_total - k) if n_total > k else 1

    # All pairwise comparisons
    for i, name_i in enumerate(group_names):
        for j, name_j in enumerate(group_names):
            if i >= j:
                continue

            group_i = groups[name_i]
            group_j = groups[name_j]

            if not group_i or not group_j:
                continue

            mean_diff = np.mean(group_i) - np.mean(group_j)
            se = np.sqrt(ms_within * (1/len(group_i) + 1/len(group_j)))

            # Compute q statistic
            q_stat = abs(mean_diff) / se if se > 0 else 0

            # Get critical q value (approximation using studentized range)
            # For proper analysis, use scipy.stats.studentized_range
            df_within = n_total - k
            try:
                # Use t-test as approximation
                t_stat = mean_diff / se if se > 0 else 0
                p_value = 2 * (1 - scipy_stats.t.cdf(abs(t_stat), df_within))
            except Exception:
                p_value = 1.0

            comparison = {
                "group1": name_i,
                "group2": name_j,
                "mean_diff": mean_diff,
                "std_error": se,
                "q_statistic": q_stat,
                "p_value": p_value,
                "significant": p_value < 0.05,
            }
            comparisons.append(comparison)

            if p_value < 0.05:
                significant_pairs.append((name_i, name_j))

    return TukeyResult(
        comparisons=comparisons,
        significant_pairs=significant_pairs,
    )


def compute_effect_sizes(
    group1: list[float],
    group2: list[float],
) -> EffectSize:
    """
    Compute effect sizes for two-group comparison.

    Args:
        group1: First group values
        group2: Second group values

    Returns:
        EffectSize with Cohen's d, Hedges' g, and interpretation
    """
    if not group1 or not group2:
        return EffectSize(cohens_d=0, hedges_g=0, interpretation="negligible")

    mean1 = np.mean(group1)
    mean2 = np.mean(group2)
    std1 = np.std(group1, ddof=1)
    std2 = np.std(group2, ddof=1)
    n1 = len(group1)
    n2 = len(group2)

    # Pooled standard deviation
    pooled_std = np.sqrt(
        ((n1 - 1) * std1**2 + (n2 - 1) * std2**2) / (n1 + n2 - 2)
    )

    # Cohen's d
    cohens_d = (mean1 - mean2) / pooled_std if pooled_std > 0 else 0

    # Hedges' g (bias-corrected)
    correction = 1 - (3 / (4 * (n1 + n2) - 9))
    hedges_g = cohens_d * correction

    # Interpretation
    abs_d = abs(cohens_d)
    if abs_d < 0.2:
        interpretation = "negligible"
    elif abs_d < 0.5:
        interpretation = "small"
    elif abs_d < 0.8:
        interpretation = "medium"
    else:
        interpretation = "large"

    return EffectSize(
        cohens_d=cohens_d,
        hedges_g=hedges_g,
        interpretation=interpretation,
    )


def compute_chi_square(
    observed: dict[str, dict[str, int]],
) -> dict:
    """
    Compute chi-square test for pattern frequency differences.

    Args:
        observed: Dict of group -> {pattern -> count}

    Returns:
        Chi-square test results
    """
    # Build contingency table
    groups = list(observed.keys())
    patterns = set()
    for counts in observed.values():
        patterns.update(counts.keys())
    patterns = list(patterns)

    if len(groups) < 2 or len(patterns) < 2:
        return {"chi2": 0, "p_value": 1.0, "significant": False}

    # Create matrix
    matrix = np.zeros((len(groups), len(patterns)))
    for i, group in enumerate(groups):
        for j, pattern in enumerate(patterns):
            matrix[i, j] = observed[group].get(pattern, 0)

    try:
        chi2, p_value, dof, expected = scipy_stats.chi2_contingency(matrix)
    except Exception:
        return {"chi2": 0, "p_value": 1.0, "significant": False}

    return {
        "chi2": chi2,
        "p_value": p_value,
        "dof": dof,
        "significant": p_value < 0.05,
        "groups": groups,
        "patterns": patterns,
    }


def kruskal_wallis(
    groups: dict[str, list[float]],
) -> dict:
    """
    Run Kruskal-Wallis test for non-normal distributions.

    Args:
        groups: Dict mapping group names to lists of values

    Returns:
        Test results
    """
    group_values = [g for g in groups.values() if g]

    if len(group_values) < 2:
        return {"H": 0, "p_value": 1.0, "significant": False}

    try:
        h_stat, p_value = scipy_stats.kruskal(*group_values)
    except Exception:
        return {"H": 0, "p_value": 1.0, "significant": False}

    return {
        "H": h_stat,
        "p_value": p_value,
        "significant": p_value < 0.05,
    }


def odds_ratio(
    group1_success: int,
    group1_total: int,
    group2_success: int,
    group2_total: int,
) -> dict:
    """
    Compute odds ratio for binary outcomes.

    Args:
        group1_success: Successes in group 1
        group1_total: Total in group 1
        group2_success: Successes in group 2
        group2_total: Total in group 2

    Returns:
        Odds ratio and confidence interval
    """
    # Avoid division by zero
    g1_fail = max(1, group1_total - group1_success)
    g2_fail = max(1, group2_total - group2_success)
    g1_succ = max(1, group1_success)
    g2_succ = max(1, group2_success)

    # Odds ratio
    or_value = (g1_succ / g1_fail) / (g2_succ / g2_fail)

    # Log odds ratio standard error
    se_log_or = np.sqrt(1/g1_succ + 1/g1_fail + 1/g2_succ + 1/g2_fail)

    # 95% CI
    log_or = np.log(or_value)
    ci_lower = np.exp(log_or - 1.96 * se_log_or)
    ci_upper = np.exp(log_or + 1.96 * se_log_or)

    return {
        "odds_ratio": or_value,
        "ci_lower": ci_lower,
        "ci_upper": ci_upper,
        "significant": ci_lower > 1 or ci_upper < 1,  # 1 not in CI
    }
