"""Tests for experiment configuration validation."""

import pytest
from pydantic import ValidationError
from harness.config.schema import (
    ModelConfig,
    MarketConfig,
    CBAConfig,
    ExperimentConfig,
    Study1Config,
    Study2Config,
)


class TestModelConfig:
    def test_valid_openai(self):
        m = ModelConfig(name="gpt-4o", provider="openai")
        assert m.provider == "openai"

    def test_valid_anthropic(self):
        m = ModelConfig(name="claude-3-5-sonnet", provider="Anthropic")
        assert m.provider == "anthropic"  # normalized to lowercase

    def test_valid_mock(self):
        m = ModelConfig(name="test", provider="mock")
        assert m.provider == "mock"

    def test_invalid_provider(self):
        with pytest.raises(ValidationError, match="Provider"):
            ModelConfig(name="x", provider="invalid_provider")

    def test_temperature_bounds(self):
        ModelConfig(name="x", provider="mock", temperature=0.0)
        ModelConfig(name="x", provider="mock", temperature=2.0)
        with pytest.raises(ValidationError):
            ModelConfig(name="x", provider="mock", temperature=-0.1)
        with pytest.raises(ValidationError):
            ModelConfig(name="x", provider="mock", temperature=2.1)

    def test_max_tokens_bounds(self):
        with pytest.raises(ValidationError):
            ModelConfig(name="x", provider="mock", max_tokens=0)
        with pytest.raises(ValidationError):
            ModelConfig(name="x", provider="mock", max_tokens=5000)


class TestMarketConfig:
    def test_defaults(self):
        m = MarketConfig()
        assert m.num_buyers == 2
        assert m.num_sellers == 2
        assert m.min_price == 0.01

    def test_max_price_must_exceed_min(self):
        with pytest.raises(ValidationError, match="max_price"):
            MarketConfig(min_price=1.0, max_price=0.5)

    def test_num_buyers_bounds(self):
        with pytest.raises(ValidationError):
            MarketConfig(num_buyers=0)
        with pytest.raises(ValidationError):
            MarketConfig(num_buyers=11)


class TestCBAConfig:
    def test_defaults(self):
        c = CBAConfig()
        assert c.enabled is True
        assert c.max_per_tx == 1.0

    def test_max_per_tx_cannot_exceed_daily(self):
        with pytest.raises(ValidationError, match="max_per_tx"):
            CBAConfig(max_per_tx=20.0, max_per_day=10.0)

    def test_max_per_day_cannot_exceed_total(self):
        with pytest.raises(ValidationError, match="max_per_day"):
            CBAConfig(max_per_day=200.0, max_total=100.0)

    def test_zero_total_means_no_limit(self):
        c = CBAConfig(max_per_day=100.0, max_total=0)
        assert c.max_total == 0  # no limit, no validation error


class TestStudyConfigs:
    def test_study1_primary_runs(self):
        s = Study1Config()
        assert s.primary_runs == 2 * 3 * 30  # 2 competitions × 3 constraints × 30 runs

    def test_study2_total_conditions(self):
        s = Study2Config()
        expected = (
            len(s.overcharger_multipliers)
            + len(s.non_delivery_modes)
            + len(s.bait_switch_modes)
            + len(s.injection_intensities)
            + len(s.sybil_counts)
        )
        assert s.total_adversary_conditions == expected


class TestExperimentConfig:
    def test_defaults(self):
        c = ExperimentConfig()
        assert c.random_seed == 42
        assert len(c.models) == 3

    def test_get_model_found(self):
        c = ExperimentConfig()
        m = c.get_model("gpt-4o")
        assert m is not None
        assert m.provider == "openai"

    def test_get_model_not_found(self):
        c = ExperimentConfig()
        assert c.get_model("nonexistent") is None

    def test_custom_seed(self):
        c = ExperimentConfig(random_seed=123)
        assert c.random_seed == 123

    def test_mock_mode(self):
        c = ExperimentConfig(mock_mode=True)
        assert c.mock_mode is True
