"""Analysis tools for experiment results."""

from .trace_analysis import (
    TraceAnalyzer,
    ReasoningPattern,
    BroadCategory,
    CodedDecision,
    FailureMode,
    PatternCoder,
    PilotAnalyzer,
    PilotTrace,
    IterativeCodebook,
    CodebookRevision,
    HumanCoding,
    compute_inter_rater_reliability,
)
from .metrics import compute_price_metrics, compute_completion_metrics
from .statistics import run_anova, tukey_hsd, compute_effect_sizes
from .dose_response import fit_dose_response, DoseResponseCurve
from .figures import create_figure_set
from .latex_export import export_to_latex, create_pattern_table

__all__ = [
    # Core trace analysis
    "TraceAnalyzer",
    "ReasoningPattern",
    "BroadCategory",
    "CodedDecision",
    "FailureMode",
    "PatternCoder",
    # Pilot analysis (use BEFORE finalizing codebook)
    "PilotAnalyzer",
    "PilotTrace",
    "IterativeCodebook",
    "CodebookRevision",
    # Inter-rater reliability
    "HumanCoding",
    "compute_inter_rater_reliability",
    # Metrics
    "compute_price_metrics",
    "compute_completion_metrics",
    # Statistics
    "run_anova",
    "tukey_hsd",
    "compute_effect_sizes",
    # Dose-response
    "fit_dose_response",
    "DoseResponseCurve",
    # Output
    "create_figure_set",
    "export_to_latex",
    "create_pattern_table",
]
