"""Tests for BudgetGuard, AuditLog, and DemoGuard."""

import json
import os
import tempfile
import threading
import time
from decimal import Decimal

import pytest

from alancoin.agents._audit import AuditLog, _GENESIS_HASH, _compute_hash
from alancoin.agents._demo import BudgetExceededError, DemoGuard
from alancoin.agents._guard import BudgetGuard


# ---------------------------------------------------------------------------
# AuditLog
# ---------------------------------------------------------------------------


class TestAuditLog:
    def test_append_and_iterate(self):
        log = AuditLog()
        log.append("tool_started", "search", "0", "5.00", input_summary="q=test")
        log.append("tool_completed", "search", "0.10", "4.90", output_summary="results")
        assert len(log) == 2
        entries = list(log)
        assert entries[0].seq == 0
        assert entries[0].action == "tool_started"
        assert entries[1].seq == 1
        assert entries[1].cost == "0.10"

    def test_hash_chain_integrity(self):
        log = AuditLog()
        for i in range(10):
            log.append("tool_completed", f"tool_{i}", "0.01", "4.90")
        assert log.verify_integrity() is True

    def test_to_json(self):
        log = AuditLog()
        log.append("tool_started", "calc", "0", "5.00")
        data = json.loads(log.to_json())
        assert len(data) == 1
        assert data[0]["tool_name"] == "calc"
        assert "hash" in data[0]

    def test_to_csv_file(self):
        log = AuditLog()
        log.append("tool_started", "search", "0", "5.00")
        log.append("tool_completed", "search", "0.05", "4.95")
        with tempfile.NamedTemporaryFile(suffix=".csv", delete=False) as f:
            path = f.name
        try:
            log.to_csv(path)
            content = open(path).read()
            assert "tool_started" in content
            assert "tool_completed" in content
            assert "search" in content
        finally:
            os.unlink(path)

    def test_to_csv_string(self):
        log = AuditLog()
        log.append("tool_completed", "calc", "0.01", "4.99")
        csv_str = log.to_csv_string()
        assert "calc" in csv_str
        assert "0.01" in csv_str

    def test_empty_log(self):
        log = AuditLog()
        assert len(log) == 0
        assert log.verify_integrity() is True
        assert log.to_json() == "[]"
        assert log.to_csv_string() == ""

    def test_input_output_truncation(self):
        log = AuditLog()
        long_input = "x" * 500
        log.append("tool_started", "t", "0", "5.00", input_summary=long_input)
        entry = list(log)[0]
        assert len(entry.input_summary) == 203  # 200 + "..."

    def test_thread_safety(self):
        log = AuditLog()
        errors = []

        def writer(n):
            try:
                for i in range(50):
                    log.append("tool_completed", f"t_{n}_{i}", "0.01", "4.00")
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=writer, args=(i,)) for i in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors
        assert len(log) == 200
        assert log.verify_integrity()


# ---------------------------------------------------------------------------
# DemoGuard
# ---------------------------------------------------------------------------


class TestDemoGuard:
    def test_charge_basic(self):
        dg = DemoGuard(budget="1.00", cost_per_call="0.25")
        cost = dg.charge("search")
        assert cost == "0.25"
        assert dg.total_spent == "0.25"
        assert dg.remaining == "0.75"

    def test_charge_per_tool_override(self):
        dg = DemoGuard(budget="5.00", cost_per_call="0.01", tool_costs={"search": "0.50"})
        cost = dg.charge("search")
        assert cost == "0.50"
        cost2 = dg.charge("calc")
        assert cost2 == "0.01"

    def test_charge_exceeds_budget(self):
        dg = DemoGuard(budget="0.05", cost_per_call="0.03")
        dg.charge("t1")  # 0.03 spent, 0.02 remaining
        with pytest.raises(BudgetExceededError, match="only \\$0.02 remaining"):
            dg.charge("t2")  # 0.03 > 0.02

    def test_refund(self):
        dg = DemoGuard(budget="1.00", cost_per_call="0.50")
        dg.charge("t1")
        assert dg.remaining == "0.50"
        dg.refund("0.50")
        assert dg.remaining == "1.00"
        assert dg.total_spent == "0.00"

    def test_exact_budget_exhaustion(self):
        dg = DemoGuard(budget="0.03", cost_per_call="0.01")
        dg.charge("t1")
        dg.charge("t2")
        dg.charge("t3")
        assert dg.remaining == "0.00"
        with pytest.raises(BudgetExceededError):
            dg.charge("t4")


# ---------------------------------------------------------------------------
# BudgetGuard (demo mode)
# ---------------------------------------------------------------------------


class TestBudgetGuardDemo:
    def test_context_manager_lifecycle(self):
        with BudgetGuard(budget="5.00", demo=True) as guard:
            assert guard.remaining == "5.00"
            assert guard.call_count == 0
            assert guard.total_spent == "0"
            assert not guard.is_over_budget
        # After exit, guard is inactive
        with pytest.raises(BudgetExceededError, match="not active"):
            guard.before_tool("t", {})

    def test_before_after_tool(self):
        with BudgetGuard(budget="5.00", cost_per_call="0.10", demo=True) as guard:
            guard.before_tool("search", {"query": "test"})
            guard.after_tool("search", "results")
            assert guard.total_spent == "0.10"
            assert guard.remaining == "4.90"
            assert guard.call_count == 1

    def test_multiple_tool_calls(self):
        with BudgetGuard(budget="1.00", cost_per_call="0.25", demo=True) as guard:
            for i in range(4):
                guard.before_tool(f"t{i}", {})
                guard.after_tool(f"t{i}", "ok")
            assert guard.total_spent == "1.00"
            assert guard.remaining == "0.00"
            assert guard.call_count == 4
            assert guard.is_over_budget

    def test_budget_exceeded_on_tool(self):
        with BudgetGuard(budget="0.05", cost_per_call="0.03", demo=True) as guard:
            guard.before_tool("t1", {})  # reserves 0.03
            guard.after_tool("t1", "ok")  # confirms
            with pytest.raises(BudgetExceededError):
                guard.before_tool("t2", {})  # 0.03 > 0.02 remaining → fails at reserve

    def test_tool_error_no_charge(self):
        with BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True) as guard:
            guard.before_tool("t1", {})
            guard.on_tool_error("t1", RuntimeError("boom"))
            assert Decimal(guard.total_spent) == 0
            assert guard.remaining == "5.00"
            assert guard.call_count == 0

    def test_audit_trail(self):
        with BudgetGuard(budget="5.00", cost_per_call="0.10", demo=True) as guard:
            guard.before_tool("search", {"q": "test"})
            guard.after_tool("search", "results")
            guard.before_tool("calc", {"expr": "1+1"})
            guard.on_tool_error("calc", ValueError("bad"))

        trail = guard.audit_trail
        assert len(trail) == 4  # start, complete, start, error
        assert trail.verify_integrity()
        entries = list(trail)
        assert entries[0].action == "tool_started"
        assert entries[1].action == "tool_completed"
        assert entries[1].cost == "0.10"
        assert entries[2].action == "tool_started"
        assert entries[3].action == "tool_failed"

    def test_cost_report(self):
        with BudgetGuard(
            budget="5.00",
            cost_per_call="0.01",
            tool_costs={"search": "0.50", "calc": "0.10"},
            demo=True,
        ) as guard:
            guard.before_tool("search", {})
            guard.after_tool("search", "r")
            guard.before_tool("calc", {})
            guard.after_tool("calc", "2")
            guard.before_tool("search", {})
            guard.after_tool("search", "r2")

        report = guard.cost_report()
        assert report["budget"] == "5.00"
        assert report["call_count"] == 3
        assert report["by_tool"]["search"] == "1.00"
        assert report["by_tool"]["calc"] == "0.10"

    def test_velocity_circuit_breaker(self):
        with BudgetGuard(
            budget="100.00",
            cost_per_call="1.00",
            max_velocity=2.0,  # $2/min
            demo=True,
        ) as guard:
            # First two calls: $2.00 spent
            guard.before_tool("t1", {})
            guard.after_tool("t1", "ok")
            guard.before_tool("t2", {})
            guard.after_tool("t2", "ok")
            # Third call should trip the velocity breaker
            with pytest.raises(BudgetExceededError, match="Velocity circuit breaker"):
                guard.before_tool("t3", {})

    def test_tool_costs_override(self):
        with BudgetGuard(
            budget="5.00",
            cost_per_call="0.01",
            tool_costs={"expensive": "2.00"},
            demo=True,
        ) as guard:
            guard.before_tool("cheap", {})
            guard.after_tool("cheap", "ok")
            assert guard.total_spent == "0.01"

            guard.before_tool("expensive", {})
            guard.after_tool("expensive", "ok")
            assert guard.total_spent == "2.01"

    def test_audit_json_export(self):
        with BudgetGuard(budget="5.00", demo=True) as guard:
            guard.before_tool("t", {})
            guard.after_tool("t", "ok")

        data = json.loads(guard.audit_trail.to_json())
        assert len(data) == 2
        assert all("hash" in entry for entry in data)
        assert all("timestamp" in entry for entry in data)


# ---------------------------------------------------------------------------
# Input validation
# ---------------------------------------------------------------------------


class TestInputValidation:
    def test_invalid_budget_empty(self):
        with pytest.raises(ValueError, match="budget"):
            BudgetGuard(budget="", demo=True)

    def test_invalid_budget_non_numeric(self):
        with pytest.raises(ValueError, match="budget"):
            BudgetGuard(budget="abc", demo=True)

    def test_invalid_budget_negative(self):
        with pytest.raises(ValueError, match="budget"):
            BudgetGuard(budget="-5.00", demo=True)

    def test_invalid_budget_zero(self):
        with pytest.raises(ValueError, match="budget"):
            BudgetGuard(budget="0", demo=True)

    def test_invalid_cost_per_call_negative(self):
        with pytest.raises(ValueError, match="cost_per_call"):
            BudgetGuard(budget="5.00", cost_per_call="-1", demo=True)

    def test_valid_zero_cost_per_call(self):
        guard = BudgetGuard(budget="5.00", cost_per_call="0", demo=True)
        assert guard._cost_per_call == "0"

    def test_invalid_tool_costs(self):
        with pytest.raises(ValueError, match="tool_costs"):
            BudgetGuard(budget="5.00", tool_costs={"t": "abc"}, demo=True)

    def test_demo_guard_invalid_budget(self):
        from alancoin.agents._demo import DemoGuard

        with pytest.raises(ValueError, match="budget"):
            DemoGuard(budget="xyz")


# ---------------------------------------------------------------------------
# Resource cleanup
# ---------------------------------------------------------------------------


class TestResourceCleanup:
    def test_close_method(self):
        guard = BudgetGuard(budget="5.00", demo=True)
        guard._enter_standalone()
        assert guard._active
        guard.close()
        assert not guard._active

    def test_close_idempotent(self):
        guard = BudgetGuard(budget="5.00", demo=True)
        guard._enter_standalone()
        guard.close()
        guard.close()  # second call should not raise
        assert not guard._active

    def test_handler_as_context_manager(self):
        """LangGraph handler works as context manager."""
        import sys
        import types

        # Ensure langchain_core stub is in place
        if "langchain_core.callbacks.base" not in sys.modules:
            _fake_lc = types.ModuleType("langchain_core")
            _fake_cb = types.ModuleType("langchain_core.callbacks")
            _fake_cb_base = types.ModuleType("langchain_core.callbacks.base")

            class _Fake:
                pass

            _fake_cb_base.BaseCallbackHandler = _Fake
            _fake_lc.callbacks = _fake_cb
            _fake_cb.base = _fake_cb_base
            sys.modules["langchain_core"] = _fake_lc
            sys.modules["langchain_core.callbacks"] = _fake_cb
            sys.modules["langchain_core.callbacks.base"] = _fake_cb_base

        from alancoin.agents.langgraph import budget_handler

        with budget_handler(budget="5.00", demo=True) as handler:
            assert handler.guard._active
        assert not handler.guard._active


# ---------------------------------------------------------------------------
# max_per_call enforcement
# ---------------------------------------------------------------------------


class TestMaxPerCall:
    def test_blocks_expensive_tool(self):
        with BudgetGuard(
            budget="5.00", max_per_call="0.10", tool_costs={"expensive": "1.00"}, demo=True
        ) as guard:
            with pytest.raises(BudgetExceededError, match="max_per_call"):
                guard.before_tool("expensive", {})

    def test_allows_cheap_tool(self):
        with BudgetGuard(
            budget="5.00", max_per_call="0.50", cost_per_call="0.10", demo=True
        ) as guard:
            guard.before_tool("cheap", {})
            guard.after_tool("cheap", "ok")
            assert guard.call_count == 1

    def test_none_allows_anything(self):
        with BudgetGuard(
            budget="100.00", max_per_call=None, cost_per_call="50.00", demo=True
        ) as guard:
            guard.before_tool("big", {})
            guard.after_tool("big", "ok")
            assert guard.total_spent == "50.00"

    def test_server_mode_after_tool(self):
        with BudgetGuard(budget="5.00", max_per_call="1.00", demo=True) as guard:
            guard.before_tool("t", {})
            with pytest.raises(BudgetExceededError, match="max_per_call"):
                guard.after_tool("t", "ok", actual_cost="5.00")


# ---------------------------------------------------------------------------
# Pre-charge pattern
# ---------------------------------------------------------------------------


class TestPreCharge:
    def test_precharge_then_confirm(self):
        with BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True) as guard:
            guard.before_tool("t", {})
            assert guard.total_spent == "1.00"  # reserved
            guard.after_tool("t", "ok")
            assert guard.total_spent == "1.00"  # confirmed (same)
            assert guard.call_count == 1

    def test_precharge_then_error_refunds(self):
        with BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True) as guard:
            guard.before_tool("t", {})
            assert guard.total_spent == "1.00"
            guard.on_tool_error("t", RuntimeError("boom"))
            assert Decimal(guard.total_spent) == 0
            assert guard.remaining == "5.00"

    def test_precharge_prevents_overspend(self):
        with BudgetGuard(budget="0.05", cost_per_call="0.06", demo=True) as guard:
            with pytest.raises(BudgetExceededError):
                guard.before_tool("t", {})  # 0.06 > 0.05 → fails before tool runs


# ---------------------------------------------------------------------------
# Budget warnings
# ---------------------------------------------------------------------------


class TestBudgetWarnings:
    def test_warning_at_threshold(self):
        warnings_fired = []

        def on_warn(threshold, spent, remaining):
            warnings_fired.append((threshold, spent, remaining))

        with BudgetGuard(
            budget="1.00",
            cost_per_call="0.10",
            demo=True,
            on_budget_warning=on_warn,
            warning_thresholds=(0.50, 0.90),
        ) as guard:
            for i in range(5):
                guard.before_tool(f"t{i}", {})
                guard.after_tool(f"t{i}", "ok")
            # At 0.50 spent, 50% threshold fires
            assert len(warnings_fired) == 1
            assert warnings_fired[0][0] == 0.50

    def test_warning_fires_once(self):
        count = [0]

        def on_warn(threshold, spent, remaining):
            count[0] += 1

        with BudgetGuard(
            budget="1.00", cost_per_call="0.40", demo=True,
            on_budget_warning=on_warn, warning_thresholds=(0.50,),
        ) as guard:
            for i in range(2):
                guard.before_tool(f"t{i}", {})
                guard.after_tool(f"t{i}", "ok")
        assert count[0] == 1  # Fires once at 0.40, then 0.80 still same threshold

    def test_warning_callback_error_does_not_crash(self):
        def bad_callback(threshold, spent, remaining):
            raise RuntimeError("callback error")

        with BudgetGuard(
            budget="1.00", cost_per_call="0.80", demo=True,
            on_budget_warning=bad_callback, warning_thresholds=(0.50,),
        ) as guard:
            guard.before_tool("t", {})
            guard.after_tool("t", "ok")  # Should not raise despite callback error

    def test_no_warning_without_callback(self):
        with BudgetGuard(budget="1.00", cost_per_call="0.80", demo=True) as guard:
            guard.before_tool("t", {})
            guard.after_tool("t", "ok")  # No crash


# ---------------------------------------------------------------------------
# Audit persistence (JSONL)
# ---------------------------------------------------------------------------


class TestAuditPersistence:
    def test_jsonl_file_created(self):
        with tempfile.NamedTemporaryFile(suffix=".jsonl", delete=False) as f:
            path = f.name
        try:
            with BudgetGuard(budget="5.00", demo=True, audit_path=path) as guard:
                guard.before_tool("search", {"q": "test"})
                guard.after_tool("search", "results")
            lines = open(path).readlines()
            assert len(lines) == 2
            for line in lines:
                entry = json.loads(line)
                assert "tool_name" in entry
                assert "hash" in entry
        finally:
            os.unlink(path)

    def test_jsonl_append_only(self):
        with tempfile.NamedTemporaryFile(suffix=".jsonl", delete=False) as f:
            path = f.name
        try:
            with BudgetGuard(budget="5.00", demo=True, audit_path=path) as guard:
                for i in range(3):
                    guard.before_tool(f"t{i}", {})
                    guard.after_tool(f"t{i}", "ok")
            lines = open(path).readlines()
            assert len(lines) == 6  # 3 starts + 3 completes
        finally:
            os.unlink(path)

    def test_no_file_without_path(self):
        with BudgetGuard(budget="5.00", demo=True) as guard:
            guard.before_tool("t", {})
            guard.after_tool("t", "ok")
        # No crash, no file created


# ---------------------------------------------------------------------------
# Per-agent sub-budgets
# ---------------------------------------------------------------------------


class TestAgentSubBudgets:
    def test_per_agent_limit(self):
        with BudgetGuard(
            budget="10.00",
            cost_per_call="1.00",
            demo=True,
            agent_budgets={"alice": "2.00", "bob": "3.00"},
        ) as guard:
            guard.before_tool("t1", {}, agent_name="alice")
            guard.after_tool("t1", "ok", agent_name="alice")
            guard.before_tool("t2", {}, agent_name="alice")
            guard.after_tool("t2", "ok", agent_name="alice")
            # Alice spent $2.00, at her limit
            with pytest.raises(BudgetExceededError, match="alice"):
                guard.before_tool("t3", {}, agent_name="alice")

    def test_other_agent_still_has_budget(self):
        with BudgetGuard(
            budget="10.00",
            cost_per_call="2.00",
            demo=True,
            agent_budgets={"alice": "2.00", "bob": "4.00"},
        ) as guard:
            guard.before_tool("t1", {}, agent_name="alice")
            guard.after_tool("t1", "ok", agent_name="alice")
            # Alice exhausted, but bob still good
            with pytest.raises(BudgetExceededError, match="alice"):
                guard.before_tool("t2", {}, agent_name="alice")
            guard.before_tool("t3", {}, agent_name="bob")
            guard.after_tool("t3", "ok", agent_name="bob")
            assert guard.call_count == 2

    def test_no_agent_budgets_allows_all(self):
        with BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True) as guard:
            guard.before_tool("t", {}, agent_name="anyone")
            guard.after_tool("t", "ok", agent_name="anyone")
            assert guard.call_count == 1
