"""Tests for statistical analysis functions with known values."""

import pytest
import numpy as np
from harness.analysis.statistics import (
    run_anova,
    tukey_hsd,
    compute_effect_sizes,
    compute_chi_square,
    kruskal_wallis,
    odds_ratio,
    run_two_way_anova,
)


class TestANOVA:
    def test_significant_difference(self):
        groups = {
            "A": [10.0, 11.0, 12.0, 10.5, 11.5],
            "B": [20.0, 21.0, 22.0, 20.5, 21.5],
        }
        result = run_anova(groups)
        assert result.significant
        assert result.p_value < 0.05
        assert result.f_statistic > 0
        assert result.effect_size_eta_sq > 0.5

    def test_no_significant_difference(self):
        groups = {
            "A": [10.0, 10.1, 9.9, 10.2, 9.8],
            "B": [10.0, 10.1, 9.9, 10.2, 9.8],
        }
        result = run_anova(groups)
        assert not result.significant

    def test_single_group(self):
        result = run_anova({"A": [1, 2, 3]})
        assert result.f_statistic == 0
        assert result.p_value == 1.0

    def test_empty_groups_handled(self):
        result = run_anova({"A": [1, 2], "B": []})
        assert result.p_value == 1.0

    def test_degrees_of_freedom(self):
        groups = {
            "A": [1.0, 2.0, 3.0],
            "B": [4.0, 5.0, 6.0],
            "C": [7.0, 8.0, 9.0],
        }
        result = run_anova(groups)
        assert result.df_between == 2  # k - 1
        assert result.df_within == 6  # n_total - k


class TestTukeyHSD:
    def test_pairwise_comparisons(self):
        groups = {
            "A": [1.0, 2.0, 3.0, 2.0, 1.5],
            "B": [10.0, 11.0, 12.0, 11.0, 10.5],
            "C": [5.0, 6.0, 7.0, 6.0, 5.5],
        }
        result = tukey_hsd(groups)
        assert len(result.comparisons) == 3  # C(3,2) = 3 pairs
        assert len(result.significant_pairs) > 0

    def test_no_data(self):
        result = tukey_hsd({"A": [], "B": []})
        assert len(result.comparisons) == 0


class TestEffectSizes:
    def test_large_effect(self):
        g1 = [1.0, 2.0, 3.0, 2.0, 1.5]
        g2 = [10.0, 11.0, 12.0, 11.0, 10.5]
        es = compute_effect_sizes(g1, g2)
        assert es.interpretation == "large"
        assert abs(es.cohens_d) > 0.8

    def test_negligible_effect(self):
        g1 = [10.0, 10.1, 9.9, 10.2, 9.8]
        g2 = [10.0, 10.1, 9.9, 10.2, 9.8]
        es = compute_effect_sizes(g1, g2)
        assert es.interpretation == "negligible"
        assert abs(es.cohens_d) < 0.2

    def test_hedges_correction(self):
        g1 = [1.0, 2.0, 3.0]
        g2 = [5.0, 6.0, 7.0]
        es = compute_effect_sizes(g1, g2)
        # Hedges' g should be slightly smaller than Cohen's d
        assert abs(es.hedges_g) < abs(es.cohens_d)

    def test_empty_groups(self):
        es = compute_effect_sizes([], [1.0, 2.0])
        assert es.interpretation == "negligible"


class TestChiSquare:
    def test_significant_association(self):
        observed = {
            "cba": {"accept": 80, "reject": 20},
            "none": {"accept": 40, "reject": 60},
        }
        result = compute_chi_square(observed)
        assert result["significant"]

    def test_no_association(self):
        observed = {
            "cba": {"accept": 50, "reject": 50},
            "none": {"accept": 50, "reject": 50},
        }
        result = compute_chi_square(observed)
        assert not result["significant"]

    def test_single_group(self):
        result = compute_chi_square({"A": {"x": 10}})
        assert result["chi2"] == 0


class TestKruskalWallis:
    def test_significant_difference(self):
        groups = {
            "A": [1.0, 2.0, 3.0, 2.0, 1.0],
            "B": [10.0, 11.0, 12.0, 11.0, 10.0],
        }
        result = kruskal_wallis(groups)
        assert result["significant"]

    def test_insufficient_groups(self):
        result = kruskal_wallis({"A": [1, 2, 3]})
        assert result["H"] == 0


class TestOddsRatio:
    def test_higher_odds(self):
        result = odds_ratio(90, 100, 50, 100)
        assert result["odds_ratio"] > 1

    def test_equal_odds(self):
        result = odds_ratio(50, 100, 50, 100)
        assert result["odds_ratio"] == pytest.approx(1.0)

    def test_significance_when_ci_excludes_one(self):
        result = odds_ratio(95, 100, 10, 100)
        assert result["significant"]


class TestTwoWayANOVA:
    def test_main_effects(self):
        data = [
            {"model": "A", "condition": "cba", "price": 1.0},
            {"model": "A", "condition": "cba", "price": 1.1},
            {"model": "A", "condition": "none", "price": 5.0},
            {"model": "A", "condition": "none", "price": 5.1},
            {"model": "B", "condition": "cba", "price": 1.2},
            {"model": "B", "condition": "cba", "price": 1.3},
            {"model": "B", "condition": "none", "price": 4.8},
            {"model": "B", "condition": "none", "price": 4.9},
        ]
        result = run_two_way_anova(data, "model", "condition", "price")
        # condition should have a large main effect
        assert "main_effect_condition" in result
        assert result["main_effect_condition"]["significant"]
        assert "cell_means" in result
