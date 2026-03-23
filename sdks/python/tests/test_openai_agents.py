"""Tests for OpenAI Agents SDK budget tool decorator."""

import asyncio
from decimal import Decimal

import pytest

from alancoin.agents._demo import BudgetExceededError
from alancoin.agents._guard import BudgetGuard
from alancoin.agents.openai_agents import async_budget_tool, budget_tool


class TestBudgetTool:
    def test_wraps_function(self):
        guard = BudgetGuard(budget="5.00", cost_per_call="0.50", demo=True)
        guard._enter_standalone()

        @budget_tool(guard)
        def search(query: str) -> str:
            return f"results for {query}"

        result = search(query="AI agents")
        assert result == "results for AI agents"
        assert guard.total_spent == "0.50"
        assert guard.call_count == 1
        guard.close()

    def test_on_error_refunds(self):
        guard = BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True)
        guard._enter_standalone()

        @budget_tool(guard)
        def failing_tool() -> str:
            raise RuntimeError("boom")

        with pytest.raises(RuntimeError, match="boom"):
            failing_tool()

        assert Decimal(guard.total_spent) == 0
        assert guard.remaining == "5.00"
        guard.close()

    def test_budget_exceeded_blocks(self):
        guard = BudgetGuard(budget="0.05", cost_per_call="0.06", demo=True)
        guard._enter_standalone()

        @budget_tool(guard)
        def expensive() -> str:
            return "should not run"

        with pytest.raises(BudgetExceededError):
            expensive()
        guard.close()

    def test_preserves_function_metadata(self):
        guard = BudgetGuard(budget="5.00", demo=True)
        guard._enter_standalone()

        @budget_tool(guard)
        def my_tool(x: int) -> int:
            """My docstring."""
            return x * 2

        assert my_tool.__name__ == "my_tool"
        assert my_tool.__doc__ == "My docstring."
        guard.close()

    def test_multiple_tools(self):
        guard = BudgetGuard(
            budget="5.00",
            cost_per_call="0.01",
            tool_costs={"search": "0.50", "calc": "0.10"},
            demo=True,
        )
        guard._enter_standalone()

        @budget_tool(guard)
        def search(q: str) -> str:
            return "results"

        @budget_tool(guard)
        def calc(expr: str) -> str:
            return "42"

        search(q="test")
        calc(expr="1+1")
        search(q="more")

        report = guard.cost_report()
        assert report["by_tool"]["search"] == "1.00"
        assert report["by_tool"]["calc"] == "0.10"
        guard.close()


class TestAsyncBudgetTool:
    def test_async_wraps_function(self):
        guard = BudgetGuard(budget="5.00", cost_per_call="0.25", demo=True)
        guard._enter_standalone()

        @async_budget_tool(guard)
        async def async_search(query: str) -> str:
            return f"async results for {query}"

        result = asyncio.run(async_search(query="test"))
        assert result == "async results for test"
        assert guard.total_spent == "0.25"
        guard.close()

    def test_async_on_error_refunds(self):
        guard = BudgetGuard(budget="5.00", cost_per_call="1.00", demo=True)
        guard._enter_standalone()

        @async_budget_tool(guard)
        async def async_fail() -> str:
            raise ValueError("async boom")

        with pytest.raises(ValueError, match="async boom"):
            asyncio.run(async_fail())

        assert Decimal(guard.total_spent) == 0
        guard.close()
