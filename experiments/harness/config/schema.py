"""
Pydantic models for experiment configuration validation.

Provides type-safe configuration with validation rules for all experiment parameters.
"""

from enum import Enum
from typing import Optional
from pydantic import BaseModel, Field, field_validator, model_validator


class ReasoningPattern(str, Enum):
    """Taxonomy of reasoning patterns observable in LLM completions."""

    # Price reasoning patterns
    PRICE_ANCHORING = "anchoring"           # References seller's stated price as baseline
    BUDGET_COMPARISON = "budget_check"       # Explicitly compares to stated budget
    VALUE_ASSESSMENT = "value_assess"        # Evaluates if service is "worth" the price
    COMPARISON_SHOPPING = "comparison"       # Compares multiple options before deciding

    # Constraint handling patterns
    CONSTRAINT_REFERENCE = "constraint_ref"  # Mentions spending limits explicitly
    TASK_PRIORITY = "task_priority"          # Prioritizes task completion over cost

    # Adversarial response patterns
    CONFABULATION = "confabulation"          # Invents justification for suspicious action
    OVERRIDE_COMPLIANCE = "override_comply"  # Acknowledges override, complies anyway
    UNCERTAINTY_EXPRESSION = "uncertain"     # Shows doubt but acts anyway
    REJECTION_REASONING = "rejection"        # Explains why rejecting suspicious offer


class ModelConfig(BaseModel):
    """Configuration for a specific LLM model."""

    name: str = Field(..., description="Model identifier (e.g., 'gpt-4o', 'claude-3-5-sonnet')")
    provider: str = Field(..., description="Provider name: 'openai', 'anthropic', or 'together'")
    temperature: float = Field(default=0.7, ge=0.0, le=2.0)
    max_tokens: int = Field(default=1024, gt=0, le=4096)

    # Cost tracking (per 1K tokens)
    cost_per_1k_input: float = Field(default=0.0, ge=0.0)
    cost_per_1k_output: float = Field(default=0.0, ge=0.0)

    @field_validator("provider")
    @classmethod
    def validate_provider(cls, v: str) -> str:
        allowed = {"openai", "anthropic", "together", "mock"}
        if v.lower() not in allowed:
            raise ValueError(f"Provider must be one of {allowed}")
        return v.lower()


class MarketConfig(BaseModel):
    """Configuration for market simulation parameters."""

    num_buyers: int = Field(default=2, ge=1, le=10)
    num_sellers: int = Field(default=2, ge=1, le=10)
    num_rounds: int = Field(default=10, ge=1)

    # Negotiation parameters
    max_negotiation_rounds: int = Field(default=5, ge=1, le=10)
    negotiation_timeout_seconds: float = Field(default=30.0, gt=0)

    # Service types available
    service_types: list[str] = Field(
        default=["inference", "translation", "code_review", "data_analysis", "summarization", "embedding"]
    )

    # Price range for services (in USDC)
    min_price: float = Field(default=0.01, gt=0)
    max_price: float = Field(default=10.0, gt=0)

    @field_validator("max_price")
    @classmethod
    def validate_price_range(cls, v: float, info) -> float:
        min_price = info.data.get("min_price", 0.01)
        if v <= min_price:
            raise ValueError("max_price must be greater than min_price")
        return v


class CBAConfig(BaseModel):
    """Configuration for Cryptographic Bounded Autonomy constraints."""

    enabled: bool = Field(default=True, description="Whether CBA is enforced")
    max_per_tx: float = Field(default=1.0, gt=0)
    max_per_day: float = Field(default=10.0, gt=0)
    max_total: float = Field(default=100.0, ge=0)  # 0 = no limit
    expires_in: str = Field(default="24h")

    # Prompt-based constraints (for comparison)
    prompt_constraints_enabled: bool = Field(default=True)

    @model_validator(mode="after")
    def validate_cba_limits(self):
        if self.max_total > 0 and self.max_per_day > self.max_total:
            raise ValueError("max_per_day cannot exceed max_total")
        if self.max_per_tx > self.max_per_day:
            raise ValueError("max_per_tx cannot exceed max_per_day")
        return self


class PreStudyConfig(BaseModel):
    """Configuration for pre-study reference price calibration."""

    num_services: int = Field(default=6, ge=1)
    prices_per_service: int = Field(default=10, ge=1)
    agents_per_price: int = Field(default=20, ge=1)

    # Price range to test
    price_multipliers: list[float] = Field(
        default=[0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0]
    )

    @property
    def total_decisions(self) -> int:
        """Total number of buy/skip decisions in pre-study."""
        return self.num_services * self.prices_per_service * self.agents_per_price


class Study1Config(BaseModel):
    """Configuration for Study 1: Baseline economic behavior."""

    # 2x3 factorial design
    competition_levels: list[str] = Field(
        default=["monopoly", "competitive"],
        description="Market structure conditions"
    )
    constraint_levels: list[str] = Field(
        default=["none", "prompt", "cba"],
        description="Constraint enforcement conditions"
    )

    # Runs per condition
    runs_per_condition: int = Field(default=30, ge=1)

    # Robustness checks
    prompt_variants: int = Field(default=2, ge=1, description="Alternative prompt framings")
    temperature_variants: list[float] = Field(
        default=[0.3, 0.7, 1.0],
        description="Temperature settings for robustness"
    )

    @property
    def primary_runs(self) -> int:
        """Number of primary experimental runs."""
        return (
            len(self.competition_levels) *
            len(self.constraint_levels) *
            self.runs_per_condition
        )

    @property
    def total_runs(self) -> int:
        """Total runs including robustness checks."""
        prompt_runs = self.runs_per_condition * self.prompt_variants
        temp_runs = self.runs_per_condition * len(self.temperature_variants)
        return self.primary_runs + prompt_runs + temp_runs


class AdversaryConfig(BaseModel):
    """Configuration for a specific adversary type."""

    type: str = Field(..., description="Adversary type identifier")
    intensity_levels: list[float] = Field(default=[1.0])
    enabled: bool = Field(default=True)


class Study2Config(BaseModel):
    """Configuration for Study 2: Adversarial resilience."""

    # Adversary types with intensity levels
    overcharger_multipliers: list[float] = Field(
        default=[1.5, 2.0, 3.0, 5.0, 10.0]
    )
    non_delivery_modes: list[str] = Field(
        default=["garbage", "empty", "plausible_wrong"]
    )
    bait_switch_modes: list[str] = Field(
        default=["related_wrong", "completely_unrelated"]
    )
    injection_intensities: list[str] = Field(
        default=["mild", "moderate", "aggressive"]
    )
    sybil_counts: list[int] = Field(
        default=[3, 10, 20]
    )

    # Defense conditions
    defense_conditions: list[str] = Field(
        default=["none", "prompt", "cba"]
    )

    # Runs per condition
    runs_per_condition: int = Field(default=15, ge=1)

    @property
    def total_adversary_conditions(self) -> int:
        """Total number of adversary × intensity combinations."""
        return (
            len(self.overcharger_multipliers) +
            len(self.non_delivery_modes) +
            len(self.bait_switch_modes) +
            len(self.injection_intensities) +
            len(self.sybil_counts)
        )

    @property
    def total_runs(self) -> int:
        """Approximate total runs for Study 2."""
        return (
            self.total_adversary_conditions *
            len(self.defense_conditions) *
            self.runs_per_condition
        )


class Study3Config(BaseModel):
    """Configuration for Study 3: Reputation dynamics (exploratory)."""

    conditions: list[str] = Field(
        default=["no_reputation", "visible_reputation", "reputation_with_history"]
    )
    rounds_per_run: int = Field(default=30, ge=1)
    runs_per_condition: int = Field(default=10, ge=1)

    @property
    def total_runs(self) -> int:
        return len(self.conditions) * self.runs_per_condition


class CostLimitsConfig(BaseModel):
    """Per-study cost limits in USD."""

    pre_study: float = Field(default=10.0, ge=0, description="Hard limit for pre-study")
    study1: float = Field(default=50.0, ge=0, description="Hard limit for study 1")
    study2: float = Field(default=100.0, ge=0, description="Hard limit for study 2")
    study3: float = Field(default=50.0, ge=0, description="Hard limit for study 3")


class LoggingConfig(BaseModel):
    """Configuration for structured logging."""

    log_dir: str = Field(default="results/economic_behavior")
    log_level: str = Field(default="INFO")
    log_llm_calls: bool = Field(default=True)
    log_transactions: bool = Field(default=True)
    log_negotiations: bool = Field(default=True)
    log_reasoning_traces: bool = Field(default=True)

    # Cost tracking
    track_costs: bool = Field(default=True)
    cost_alert_threshold: float = Field(default=10.0, ge=0)
    cost_limits: CostLimitsConfig = Field(default_factory=CostLimitsConfig)


class ExperimentConfig(BaseModel):
    """Root configuration for the experiment harness."""

    # Experiment metadata
    experiment_name: str = Field(default="economic_behavior")
    random_seed: int = Field(default=42)
    mock_mode: bool = Field(default=False, description="Use mock LLM/market for testing")

    # Model configurations
    models: list[ModelConfig] = Field(
        default_factory=lambda: [
            ModelConfig(
                name="gpt-4o",
                provider="openai",
                cost_per_1k_input=0.005,
                cost_per_1k_output=0.015,
            ),
            ModelConfig(
                name="claude-3-5-sonnet-20241022",
                provider="anthropic",
                cost_per_1k_input=0.003,
                cost_per_1k_output=0.015,
            ),
            ModelConfig(
                name="meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
                provider="together",
                cost_per_1k_input=0.00088,
                cost_per_1k_output=0.00088,
            ),
        ]
    )

    # Component configurations
    market: MarketConfig = Field(default_factory=MarketConfig)
    cba: CBAConfig = Field(default_factory=CBAConfig)
    logging: LoggingConfig = Field(default_factory=LoggingConfig)

    # Study-specific configurations
    pre_study: PreStudyConfig = Field(default_factory=PreStudyConfig)
    study1: Study1Config = Field(default_factory=Study1Config)
    study2: Study2Config = Field(default_factory=Study2Config)
    study3: Study3Config = Field(default_factory=Study3Config)

    # API configuration (loaded from environment)
    api_base_url: str = Field(default="http://localhost:8080")

    def get_model(self, name: str) -> Optional[ModelConfig]:
        """Get model config by name."""
        for model in self.models:
            if model.name == name:
                return model
        return None
