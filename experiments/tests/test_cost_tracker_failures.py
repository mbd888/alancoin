"""Tests for cost tracker failure modes: concurrent limit enforcement,
pre-check prevents overshoot, edge cases."""

import threading
import pytest

from harness.logging.cost_tracker import CostTracker, CostLimitExceeded


class TestPreCheckPreventsOvershoot:
    """Hard limit is checked BEFORE committing, so no overshoot is possible."""

    def test_single_call_over_limit_rejected(self):
        ct = CostTracker(hard_limit=0.01)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        with pytest.raises(CostLimitExceeded):
            ct.record_call("gpt-4o", 10000, 10000)
        # Cost should NOT have been committed
        assert ct.total_cost_usd == 0.0

    def test_second_call_pushes_over_limit(self):
        ct = CostTracker(hard_limit=0.05)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)
        # First call: well under limit
        ct.record_call("gpt-4o", 1000, 1000)
        first_cost = ct.total_cost_usd
        assert first_cost < 0.05
        # Second call: would push over limit
        with pytest.raises(CostLimitExceeded) as exc_info:
            ct.record_call("gpt-4o", 10000, 10000)
        # Cost should still be at first_cost — second was not committed
        assert ct.total_cost_usd == first_cost
        assert exc_info.value.total_cost > 0.05  # Projected total exceeds limit

    def test_exactly_at_limit_raises(self):
        ct = CostTracker(hard_limit=0.02)
        ct.register_model("m", "p", 0.01, 0.01)
        # 1000 input + 1000 output at $0.01/1k each = $0.02
        with pytest.raises(CostLimitExceeded):
            ct.record_call("m", 1000, 1000)
        assert ct.total_cost_usd == 0.0


class TestConcurrentLimitEnforcement:
    """Multiple threads cannot collectively exceed the hard limit."""

    def test_concurrent_threads_respect_limit(self):
        ct = CostTracker(hard_limit=0.10)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)

        results = {"succeeded": 0, "rejected": 0}
        lock = threading.Lock()

        def worker():
            for _ in range(50):
                try:
                    ct.record_call("gpt-4o", 100, 100)
                    with lock:
                        results["succeeded"] += 1
                except CostLimitExceeded:
                    with lock:
                        results["rejected"] += 1

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        # Total cost must not exceed limit
        assert ct.total_cost_usd < 0.10
        # Some calls should have been rejected
        assert results["rejected"] > 0
        # Total calls = succeeded + rejected
        assert results["succeeded"] + results["rejected"] == 200

    def test_no_limit_allows_all(self):
        ct = CostTracker(hard_limit=None)
        ct.register_model("gpt-4o", "openai", 0.005, 0.015)

        errors = []

        def worker():
            for _ in range(50):
                try:
                    ct.record_call("gpt-4o", 100, 100)
                except CostLimitExceeded:
                    errors.append(True)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert len(errors) == 0
        assert ct.total_calls == 200


class TestAlertThreshold:
    """Soft alert fires once, doesn't block execution."""

    def test_alert_fires_at_threshold(self):
        ct = CostTracker(alert_threshold=0.01, hard_limit=None)
        ct.register_model("m", "p", 0.01, 0.01)
        # This will cost $0.02, exceeding the $0.01 alert threshold
        ct.record_call("m", 1000, 1000)
        alerts = ct.get_alerts()
        assert len(alerts) == 1
        assert alerts[0].current_cost >= 0.01

    def test_alert_fires_only_once(self):
        ct = CostTracker(alert_threshold=0.001, hard_limit=None)
        ct.register_model("m", "p", 0.01, 0.01)
        ct.record_call("m", 1000, 1000)
        ct.record_call("m", 1000, 1000)
        ct.record_call("m", 1000, 1000)
        assert len(ct.get_alerts()) == 1


class TestCostLimitExceededException:
    """CostLimitExceeded carries useful context."""

    def test_has_total_and_limit(self):
        e = CostLimitExceeded(total_cost=15.0, hard_limit=10.0)
        assert e.total_cost == 15.0
        assert e.hard_limit == 10.0

    def test_has_partial_results(self):
        e = CostLimitExceeded(1.0, 0.5, partial_results={"runs": 3})
        assert e.partial_results == {"runs": 3}

    def test_default_partial_results(self):
        e = CostLimitExceeded(1.0, 0.5)
        assert e.partial_results == {}

    def test_str_includes_amounts(self):
        e = CostLimitExceeded(15.50, 10.00)
        msg = str(e)
        assert "$15.50" in msg
        assert "$10.00" in msg
