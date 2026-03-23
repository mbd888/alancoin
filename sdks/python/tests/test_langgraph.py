"""Tests for LangGraph budget handler integration.

Mocks the BaseCallbackHandler to avoid requiring langchain-core as a
test dependency. Tests the callback dispatch logic and budget enforcement.
"""

import sys
import types
from decimal import Decimal
from unittest.mock import MagicMock
from uuid import uuid4

import pytest

# Stub langchain_core so the import in langgraph.py succeeds without the
# actual dependency installed.
_fake_lc = types.ModuleType("langchain_core")
_fake_cb = types.ModuleType("langchain_core.callbacks")
_fake_cb_base = types.ModuleType("langchain_core.callbacks.base")


class _FakeBaseCallbackHandler:
    pass


_fake_cb_base.BaseCallbackHandler = _FakeBaseCallbackHandler
_fake_lc.callbacks = _fake_cb
_fake_cb.base = _fake_cb_base

sys.modules.setdefault("langchain_core", _fake_lc)
sys.modules.setdefault("langchain_core.callbacks", _fake_cb)
sys.modules.setdefault("langchain_core.callbacks.base", _fake_cb_base)

from alancoin.agents._demo import BudgetExceededError
from alancoin.agents._guard import BudgetGuard
from alancoin.agents.langgraph import AlancoinBudgetHandler, budget_handler


class TestAlancoinBudgetHandler:
    def _make_handler(self, **kwargs):
        kwargs.setdefault("cost_per_call", "0.10")
        guard = BudgetGuard(budget="5.00", demo=True, **kwargs)
        guard._enter_standalone()
        return AlancoinBudgetHandler(guard)

    def test_tool_start_end_cycle(self):
        handler = self._make_handler()
        run_id = uuid4()

        handler.on_tool_start(
            {"name": "search"},
            "query=test",
            run_id=run_id,
        )
        handler.on_tool_end(
            "some results",
            run_id=run_id,
        )

        assert handler.guard.call_count == 1
        assert handler.guard.total_spent == "0.10"

    def test_multiple_tools(self):
        handler = self._make_handler()

        for i in range(5):
            rid = uuid4()
            handler.on_tool_start({"name": f"tool_{i}"}, f"input_{i}", run_id=rid)
            handler.on_tool_end(f"output_{i}", run_id=rid)

        assert handler.guard.call_count == 5
        assert handler.guard.total_spent == "0.50"

    def test_tool_error_no_charge(self):
        handler = self._make_handler()
        rid = uuid4()

        handler.on_tool_start({"name": "search"}, "q=test", run_id=rid)
        handler.on_tool_error(RuntimeError("network timeout"), run_id=rid)

        assert handler.guard.call_count == 0
        assert Decimal(handler.guard.total_spent) == 0
        assert handler.guard.remaining == "5.00"

    def test_budget_exceeded_before_tool(self):
        handler = self._make_handler(cost_per_call="3.00")

        # First call: $3.00 reserved + confirmed
        rid1 = uuid4()
        handler.on_tool_start({"name": "t1"}, "in", run_id=rid1)
        handler.on_tool_end("out", run_id=rid1)

        # Second call: $3.00 > $2.00 remaining → fails at reservation
        rid2 = uuid4()
        with pytest.raises(BudgetExceededError):
            handler.on_tool_start({"name": "t2"}, "in", run_id=rid2)

    def test_actual_cost_from_gateway_metadata(self):
        handler = self._make_handler()
        rid = uuid4()

        handler.on_tool_start({"name": "search"}, "q", run_id=rid)
        # Simulate gateway response with _gateway metadata
        handler.on_tool_end(
            {"output": "results", "_gateway": {"amountPaid": "0.42"}},
            run_id=rid,
        )

        assert handler.guard.total_spent == "0.42"

    def test_audit_trail_populated(self):
        handler = self._make_handler()

        rid = uuid4()
        handler.on_tool_start({"name": "calc"}, "1+1", run_id=rid)
        handler.on_tool_end("2", run_id=rid)

        trail = handler.guard.audit_trail
        assert len(trail) == 2
        assert trail.verify_integrity()

    def test_unknown_tool_name_fallback(self):
        handler = self._make_handler()
        rid = uuid4()

        # Missing "name" key in serialized dict
        handler.on_tool_start({}, "input", run_id=rid)
        handler.on_tool_end("output", run_id=rid)

        entries = list(handler.guard.audit_trail)
        assert entries[0].tool_name == "unknown"

    def test_concurrent_tool_calls(self):
        handler = self._make_handler()
        rid1, rid2 = uuid4(), uuid4()

        handler.on_tool_start({"name": "search"}, "q1", run_id=rid1)
        handler.on_tool_start({"name": "calc"}, "1+1", run_id=rid2)
        handler.on_tool_end("r1", run_id=rid1)
        handler.on_tool_end("2", run_id=rid2)

        assert handler.guard.call_count == 2


class TestBudgetHandlerFactory:
    def test_creates_handler_with_demo_guard(self):
        handler = budget_handler(budget="10.00", demo=True, cost_per_call="0.50")
        assert isinstance(handler, AlancoinBudgetHandler)
        assert handler.guard.remaining == "10.00"

    def test_handler_is_functional(self):
        handler = budget_handler(budget="2.00", demo=True, cost_per_call="0.50")
        rid = uuid4()
        handler.on_tool_start({"name": "t"}, "in", run_id=rid)
        handler.on_tool_end("out", run_id=rid)
        assert handler.guard.total_spent == "0.50"
        assert handler.guard.remaining == "1.50"
