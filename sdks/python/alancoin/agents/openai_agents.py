"""OpenAI Agents SDK budget enforcement via tool decorators.

Wraps individual tool functions with budget tracking. Works with any
tool decorator pattern including OpenAI Agents SDK's ``function_tool``.

Usage::

    from alancoin.agents import BudgetGuard
    from alancoin.agents.openai_agents import budget_tool

    guard = BudgetGuard(budget="5.00", demo=True)
    guard._enter_standalone()

    @budget_tool(guard)
    def search(query: str) -> str:
        return "results"

    result = search(query="AI agents")
    print(guard.cost_report())
    guard.close()

Requires: ``pip install alancoin[openai-agents]``
"""

import asyncio
import functools
import logging
from typing import Any, Callable

from ._guard import BudgetGuard

logger = logging.getLogger(__name__)


def budget_tool(guard: BudgetGuard) -> Callable:
    """Decorator that wraps a tool function with budget enforcement.

    Charges budget in :meth:`before_tool` (pre-charge), confirms in
    :meth:`after_tool`, and refunds on error.

    Args:
        guard: The :class:`BudgetGuard` instance to track against.

    Returns:
        A decorator that wraps the tool function.
    """

    def decorator(func: Callable) -> Callable:
        tool_name = getattr(func, "__name__", "unknown")

        @functools.wraps(func)
        def wrapper(*args: Any, **kwargs: Any) -> Any:
            guard.before_tool(tool_name, kwargs or {"args": args})
            try:
                result = func(*args, **kwargs)
            except Exception as e:
                guard.on_tool_error(tool_name, e)
                raise
            guard.after_tool(tool_name, str(result))
            return result

        return wrapper

    return decorator


def async_budget_tool(guard: BudgetGuard) -> Callable:
    """Async version of :func:`budget_tool` for async tool functions.

    Args:
        guard: The :class:`BudgetGuard` instance to track against.

    Returns:
        A decorator that wraps the async tool function.
    """

    def decorator(func: Callable) -> Callable:
        tool_name = getattr(func, "__name__", "unknown")

        @functools.wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> Any:
            guard.before_tool(tool_name, kwargs or {"args": args})
            try:
                result = await func(*args, **kwargs)
            except Exception as e:
                guard.on_tool_error(tool_name, e)
                raise
            guard.after_tool(tool_name, str(result))
            return result

        return wrapper

    return decorator
