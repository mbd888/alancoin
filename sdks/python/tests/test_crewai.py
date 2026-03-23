"""Tests for CrewAI budget hook integration.

Mocks the crewai.hooks module to avoid requiring crewai as a test
dependency. Tests the hook registration and budget enforcement logic.
"""

import sys
import types
from dataclasses import dataclass
from unittest.mock import MagicMock

import pytest

# ---------------------------------------------------------------------------
# Stub crewai.hooks so the import succeeds without crewai installed.
# ---------------------------------------------------------------------------

_registered_before_hooks = []
_registered_after_hooks = []


def _mock_register_before(hook):
    _registered_before_hooks.append(hook)


def _mock_register_after(hook):
    _registered_after_hooks.append(hook)


_fake_crewai = types.ModuleType("crewai")
_fake_hooks = types.ModuleType("crewai.hooks")
_fake_hooks.register_before_tool_call_hook = _mock_register_before
_fake_hooks.register_after_tool_call_hook = _mock_register_after
_fake_crewai.hooks = _fake_hooks

sys.modules["crewai"] = _fake_crewai
sys.modules["crewai.hooks"] = _fake_hooks


from alancoin.agents._demo import BudgetExceededError
from alancoin.agents.crewai import enable_budget


@dataclass
class FakeToolContext:
    """Mimics CrewAI's ToolCallHookContext."""

    tool_name: str
    tool_input: dict
    tool_result: str = ""


class TestEnableBudget:
    def setup_method(self):
        _registered_before_hooks.clear()
        _registered_after_hooks.clear()

    def test_registers_hooks(self):
        guard = enable_budget(budget="5.00", demo=True)
        assert len(_registered_before_hooks) == 1
        assert len(_registered_after_hooks) == 1
        assert guard is not None

    def test_before_hook_allows_within_budget(self):
        guard = enable_budget(budget="5.00", cost_per_call="0.10", demo=True)
        before_hook = _registered_before_hooks[-1]

        ctx = FakeToolContext(tool_name="search", tool_input={"q": "test"})
        result = before_hook(ctx)
        assert result is None  # None = allow

    def test_after_hook_records_cost(self):
        guard = enable_budget(budget="5.00", cost_per_call="0.50", demo=True)
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        ctx = FakeToolContext(tool_name="search", tool_input={"q": "test"}, tool_result="results")
        before_hook(ctx)
        after_hook(ctx)

        assert guard.total_spent == "0.50"
        assert guard.call_count == 1

    def test_multiple_tool_calls(self):
        guard = enable_budget(budget="5.00", cost_per_call="0.25", demo=True)
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        for i in range(4):
            ctx = FakeToolContext(
                tool_name=f"tool_{i}",
                tool_input={"i": i},
                tool_result=f"result_{i}",
            )
            before_hook(ctx)
            after_hook(ctx)

        assert guard.call_count == 4
        assert guard.total_spent == "1.00"

    def test_budget_exceeded_blocks_tool(self):
        guard = enable_budget(budget="0.05", cost_per_call="0.03", demo=True)
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        # First call: $0.03 reserved + confirmed
        ctx1 = FakeToolContext(tool_name="t1", tool_input={}, tool_result="ok")
        before_hook(ctx1)
        after_hook(ctx1)

        # Second call: before_hook tries to reserve 0.03 but only 0.02 remains
        ctx2 = FakeToolContext(tool_name="t2", tool_input={}, tool_result="ok")
        result = before_hook(ctx2)  # returns False (budget exceeded, caught)
        assert result is False

    def test_cost_report(self):
        guard = enable_budget(
            budget="10.00",
            cost_per_call="0.10",
            tool_costs={"search": "0.50"},
            demo=True,
        )
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        for name in ["search", "calc", "search"]:
            ctx = FakeToolContext(tool_name=name, tool_input={}, tool_result="r")
            before_hook(ctx)
            after_hook(ctx)

        report = guard.cost_report()
        assert report["call_count"] == 3
        assert report["by_tool"]["search"] == "1.00"
        assert report["by_tool"]["calc"] == "0.10"

    def test_audit_trail_integrity(self):
        guard = enable_budget(budget="5.00", demo=True)
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        ctx = FakeToolContext(tool_name="search", tool_input={"q": "x"}, tool_result="y")
        before_hook(ctx)
        after_hook(ctx)

        assert guard.audit_trail.verify_integrity()
        assert len(guard.audit_trail) == 2

    def test_string_tool_input(self):
        """CrewAI sometimes passes tool_input as a string."""
        guard = enable_budget(budget="5.00", demo=True)
        before_hook = _registered_before_hooks[-1]
        after_hook = _registered_after_hooks[-1]

        ctx = FakeToolContext(tool_name="search", tool_input="raw string input", tool_result="r")
        result = before_hook(ctx)
        assert result is None  # should not crash
        after_hook(ctx)
        assert guard.call_count == 1
