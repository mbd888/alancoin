"""Tests for CostTracker thread-safe cost tracking."""

import threading
import pytest
from harness.logging.cost_tracker import CostTracker, CostLimitExceeded, ModelCosts


class TestModelCosts:
    def test_compute_cost(self):
        mc = ModelCosts(
            model="gpt-4o", provider="openai",
            cost_per_1k_input=0.005, cost_per_1k_output=0.015,
        )
        cost = mc.compute_cost(1000, 1000)
        assert cost == pytest.approx(0.005 + 0.015)

    def test_add_call(self):
        mc = ModelCosts(
            model="gpt-4o", provider="openai",
            cost_per_1k_input=0.005, cost_per_1k_output=0.015,
        )
        cost = mc.add_call(500, 200, latency_ms=100.0)
        assert cost == pytest.approx((500 / 1000) * 0.005 + (200 / 1000) * 0.015)
        assert mc.total_calls == 1
        assert mc.total_input_tokens == 500
        assert mc.total_output_tokens == 200

    def test_avg_latency(self):
        mc = ModelCosts(
            model="gpt-4o", provider="openai",
            cost_per_1k_input=0.0, cost_per_1k_output=0.0,
        )
        mc.add_call(100, 100, latency_ms=200.0)
        mc.add_call(100, 100, latency_ms=400.0)
        assert mc.avg_latency_ms == pytest.approx(300.0)

    def test_avg_latency_zero_calls(self):
        mc = ModelCosts(model="x", provider="x", cost_per_1k_input=0, cost_per_1k_output=0)
        assert mc.avg_latency_ms == 0.0

    def test_avg_tokens_per_call(self):
        mc = ModelCosts(model="x", provider="x", cost_per_1k_input=0, cost_per_1k_output=0)
        mc.add_call(100, 50)
        mc.add_call(200, 100)
        assert mc.avg_tokens_per_call == pytest.approx(225.0)


class TestCostTracker:
    def test_register_and_record(self):
        ct = CostTracker()
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        cost = ct.record_call("gpt-4o", 1000, 500, latency_ms=100)
        assert cost > 0
        assert ct.total_cost_usd == cost

    def test_unregistered_model_uses_defaults(self):
        ct = CostTracker()
        cost = ct.record_call("unknown-model", 1000, 500)
        assert cost > 0  # Uses conservative defaults

    def test_total_calls(self):
        ct = CostTracker()
        ct.register_model("m1", "p1", 0.01, 0.01)
        ct.register_model("m2", "p2", 0.01, 0.01)
        ct.record_call("m1", 100, 100)
        ct.record_call("m2", 100, 100)
        ct.record_call("m1", 100, 100)
        assert ct.total_calls == 3

    def test_alert_threshold(self):
        ct = CostTracker(alert_threshold=0.01)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        ct.record_call("gpt-4o", 1000, 1000)  # ~$0.02
        alerts = ct.get_alerts()
        assert len(alerts) == 1
        assert "threshold" in alerts[0].message.lower()

    def test_alert_triggers_once(self):
        ct = CostTracker(alert_threshold=0.001)
        ct.register_model("m", "p", 0.01, 0.01)
        ct.record_call("m", 1000, 1000)
        ct.record_call("m", 1000, 1000)
        assert len(ct.get_alerts()) == 1

    def test_hard_limit_raises(self):
        ct = CostTracker(hard_limit=0.01)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        with pytest.raises(CostLimitExceeded):
            ct.record_call("gpt-4o", 1000, 1000)  # exceeds $0.01

    def test_cost_limit_exceeded_has_attrs(self):
        ct = CostTracker(hard_limit=0.01)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        with pytest.raises(CostLimitExceeded) as exc_info:
            ct.record_call("gpt-4o", 1000, 1000)
        assert exc_info.value.hard_limit == 0.01
        assert exc_info.value.total_cost > 0.01

    def test_estimate_cost(self):
        ct = CostTracker()
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        est = ct.estimate_cost(num_runs=10)
        assert est > 0

    def test_progress_report(self):
        ct = CostTracker(hard_limit=100.0)
        ct.register_model("m", "p", 0.01, 0.01)
        ct.record_call("m", 100, 100)
        report = ct.progress_report(5, 10)
        assert "5/10" in report
        assert "50%" in report
        assert "$100.00 limit" in report

    def test_report_format(self):
        ct = CostTracker()
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        ct.record_call("gpt-4o", 100, 50)
        report = ct.report()
        assert "COST REPORT" in report
        assert "gpt-4o" in report
        assert "TOTAL" in report

    def test_to_dict(self):
        ct = CostTracker(alert_threshold=100.0)
        ct.register_model("m1", "p1", 0.01, 0.01)
        ct.record_call("m1", 100, 100)
        d = ct.to_dict()
        assert "total_cost_usd" in d
        assert "models" in d
        assert "m1" in d["models"]

    def test_thread_safety(self):
        ct = CostTracker(hard_limit=1000.0)
        ct.register_model("m", "p", 0.001, 0.001)

        def worker():
            for _ in range(100):
                ct.record_call("m", 10, 10)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert ct.total_calls == 400

    def test_get_model_summary(self):
        ct = CostTracker()
        ct.register_model("m1", "p1", 0.01, 0.01)
        ct.record_call("m1", 100, 100)
        summary = ct.get_model_summary("m1")
        assert summary is not None
        assert summary.total_calls == 1

    def test_get_model_summary_not_found(self):
        ct = CostTracker()
        assert ct.get_model_summary("nonexistent") is None
