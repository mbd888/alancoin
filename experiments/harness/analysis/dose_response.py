"""
Dose-response curve fitting for adversarial analysis.

Fits sigmoid curves to exploitation rate vs. adversary intensity data
to find ED50 (intensity at which 50% exploitation occurs).
"""

from dataclasses import dataclass
from typing import Optional
import numpy as np
from scipy.optimize import curve_fit


@dataclass
class DoseResponseCurve:
    """Fitted dose-response curve parameters."""

    # Curve parameters (4-parameter logistic)
    min_response: float  # Lower asymptote
    max_response: float  # Upper asymptote
    ed50: float  # Intensity at 50% effect
    hill_slope: float  # Steepness of curve

    # Fit quality
    r_squared: float
    rmse: float

    # Data
    doses: list[float]
    responses: list[float]
    fitted_responses: list[float]


def sigmoid(x: np.ndarray, bottom: float, top: float, ec50: float, slope: float) -> np.ndarray:
    """
    4-parameter logistic sigmoid function.

    Args:
        x: Dose/intensity values
        bottom: Lower asymptote
        top: Upper asymptote
        ec50: Dose at 50% effect
        slope: Hill slope

    Returns:
        Predicted response values
    """
    return bottom + (top - bottom) / (1 + (ec50 / x) ** slope)


def fit_dose_response(
    doses: list[float],
    responses: list[float],
    initial_guess: Optional[tuple] = None,
) -> DoseResponseCurve:
    """
    Fit dose-response curve to data.

    Args:
        doses: List of dose/intensity values
        responses: List of response values (e.g., exploitation rates)
        initial_guess: Initial parameters (bottom, top, ec50, slope)

    Returns:
        DoseResponseCurve with fitted parameters
    """
    doses_arr = np.array(doses)
    responses_arr = np.array(responses)

    # Initial guess based on data
    if initial_guess is None:
        initial_guess = (
            min(responses),  # bottom
            max(responses),  # top
            np.median(doses),  # ec50
            1.0,  # slope
        )

    # Bounds for parameters
    bounds = (
        [0, 0, min(doses) * 0.1, 0.1],  # Lower bounds
        [1, 1, max(doses) * 10, 10],  # Upper bounds
    )

    try:
        popt, pcov = curve_fit(
            sigmoid,
            doses_arr,
            responses_arr,
            p0=initial_guess,
            bounds=bounds,
            maxfev=5000,
        )

        bottom, top, ec50, slope = popt

        # Compute fitted values
        fitted = sigmoid(doses_arr, *popt)

        # R-squared
        ss_res = np.sum((responses_arr - fitted) ** 2)
        ss_tot = np.sum((responses_arr - np.mean(responses_arr)) ** 2)
        r_squared = 1 - (ss_res / ss_tot) if ss_tot > 0 else 0

        # RMSE
        rmse = np.sqrt(np.mean((responses_arr - fitted) ** 2))

        return DoseResponseCurve(
            min_response=bottom,
            max_response=top,
            ed50=ec50,
            hill_slope=slope,
            r_squared=r_squared,
            rmse=rmse,
            doses=list(doses_arr),
            responses=list(responses_arr),
            fitted_responses=list(fitted),
        )

    except Exception as e:
        # Fall back to linear interpolation
        return _linear_fallback(doses, responses)


def _linear_fallback(
    doses: list[float],
    responses: list[float],
) -> DoseResponseCurve:
    """Linear fallback when curve fitting fails."""
    doses_arr = np.array(doses)
    responses_arr = np.array(responses)

    # Simple linear fit
    slope, intercept = np.polyfit(doses_arr, responses_arr, 1)
    fitted = slope * doses_arr + intercept

    # Estimate ED50 from linear fit
    ed50 = (0.5 - intercept) / slope if slope != 0 else np.median(doses)

    # R-squared
    ss_res = np.sum((responses_arr - fitted) ** 2)
    ss_tot = np.sum((responses_arr - np.mean(responses_arr)) ** 2)
    r_squared = 1 - (ss_res / ss_tot) if ss_tot > 0 else 0

    return DoseResponseCurve(
        min_response=min(responses),
        max_response=max(responses),
        ed50=ed50,
        hill_slope=slope,
        r_squared=r_squared,
        rmse=np.sqrt(np.mean((responses_arr - fitted) ** 2)),
        doses=list(doses_arr),
        responses=list(responses_arr),
        fitted_responses=list(fitted),
    )


def analyze_dose_response_by_defense(
    data: list[dict],
    dose_field: str = "intensity",
    response_field: str = "exploited",
    defense_field: str = "defense",
) -> dict[str, DoseResponseCurve]:
    """
    Fit dose-response curves separately for each defense condition.

    Args:
        data: List of observation dicts
        dose_field: Field name for dose/intensity
        response_field: Field name for response (binary or rate)
        defense_field: Field name for defense condition

    Returns:
        Dict mapping defense condition to fitted curve
    """
    # Group by defense condition
    by_defense = {}
    for obs in data:
        defense = obs.get(defense_field, "none")
        if defense not in by_defense:
            by_defense[defense] = []
        by_defense[defense].append(obs)

    results = {}

    for defense, observations in by_defense.items():
        # Group by dose and compute response rate
        by_dose = {}
        for obs in observations:
            dose = obs.get(dose_field, 0)
            response = obs.get(response_field, 0)
            if isinstance(response, bool):
                response = 1 if response else 0

            if dose not in by_dose:
                by_dose[dose] = []
            by_dose[dose].append(response)

        # Compute rates
        doses = sorted(by_dose.keys())
        responses = [np.mean(by_dose[d]) for d in doses]

        if len(doses) >= 3:
            results[defense] = fit_dose_response(doses, responses)
        else:
            # Not enough data points
            results[defense] = DoseResponseCurve(
                min_response=min(responses) if responses else 0,
                max_response=max(responses) if responses else 1,
                ed50=np.median(doses) if doses else 1,
                hill_slope=1,
                r_squared=0,
                rmse=0,
                doses=doses,
                responses=responses,
                fitted_responses=responses,
            )

    return results


def compare_ed50(
    curve1: DoseResponseCurve,
    curve2: DoseResponseCurve,
) -> dict:
    """
    Compare ED50 values between two curves.

    Args:
        curve1: First dose-response curve
        curve2: Second dose-response curve

    Returns:
        Comparison metrics
    """
    ed50_ratio = curve1.ed50 / curve2.ed50 if curve2.ed50 > 0 else float("inf")

    return {
        "ed50_1": curve1.ed50,
        "ed50_2": curve2.ed50,
        "ed50_ratio": ed50_ratio,
        "ed50_diff": curve1.ed50 - curve2.ed50,
        "shift_direction": "right" if ed50_ratio > 1 else "left",
        "interpretation": _interpret_ed50_ratio(ed50_ratio),
    }


def _interpret_ed50_ratio(ratio: float) -> str:
    """Interpret ED50 ratio."""
    if ratio > 2:
        return "substantial protection"
    elif ratio > 1.5:
        return "moderate protection"
    elif ratio > 1:
        return "slight protection"
    elif ratio > 0.5:
        return "slight vulnerability"
    else:
        return "substantial vulnerability"


def pattern_shift_analysis(
    data: list[dict],
    dose_field: str = "intensity",
    pattern_field: str = "patterns",
) -> dict:
    """
    Analyze how reasoning patterns shift with increasing dose.

    Args:
        data: List of observation dicts
        dose_field: Field name for dose/intensity
        pattern_field: Field name for detected patterns

    Returns:
        Pattern shift analysis results
    """
    # Group by dose
    by_dose = {}
    for obs in data:
        dose = obs.get(dose_field, 0)
        patterns = obs.get(pattern_field, [])

        if dose not in by_dose:
            by_dose[dose] = []
        by_dose[dose].extend(patterns)

    # Compute pattern frequencies by dose
    doses = sorted(by_dose.keys())
    pattern_trends = {}

    for dose in doses:
        patterns = by_dose[dose]
        total = len(patterns) if patterns else 1

        freq = {}
        for p in set(patterns):
            freq[p] = patterns.count(p) / total

        for p, f in freq.items():
            if p not in pattern_trends:
                pattern_trends[p] = {"doses": [], "frequencies": []}
            pattern_trends[p]["doses"].append(dose)
            pattern_trends[p]["frequencies"].append(f)

    return {
        "doses": doses,
        "pattern_trends": pattern_trends,
    }
