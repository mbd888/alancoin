"""CrewAI budget enforcement via tool call hooks.

Registers global hooks that intercept every CrewAI tool call and enforce
Alancoin budget limits. Zero changes to existing tools or crews required.

Usage (3 lines)::

    from alancoin.agents.crewai import enable_budget

    guard = enable_budget(budget="10.00", demo=True)
    result = crew.kickoff()

After execution::

    print(guard.total_spent)            # "$3.45"
    print(guard.cost_report())          # per-tool breakdown
    print(guard.audit_trail.to_json())  # EU AI Act export

Requires: ``pip install alancoin[crewai]``
"""

import logging
from typing import Any, Dict, Optional

from ._demo import BudgetExceededError
from ._guard import BudgetGuard

logger = logging.getLogger(__name__)


def enable_budget(
    budget: str = "5.00",
    max_per_call: str = None,
    demo: bool = False,
    agent_budgets: Optional[Dict[str, str]] = None,
    **kwargs: Any,
) -> BudgetGuard:
    """Enable Alancoin budget enforcement for all CrewAI tool calls.

    Registers ``before_tool_call`` and ``after_tool_call`` hooks that
    check and record budget for every tool invocation across all agents
    in the crew.

    Call this **before** ``crew.kickoff()``.

    Example::

        from alancoin.agents.crewai import enable_budget

        guard = enable_budget(budget="10.00", demo=True)
        result = crew.kickoff()
        print(guard.cost_report())

    Args:
        budget: Total USDC budget for this workflow.
        max_per_call: Max USDC per tool call.
        demo: If True, use in-memory budget (no server needed).
        **kwargs: Forwarded to :class:`BudgetGuard`
            (``url``, ``api_key``, ``cost_per_call``, ``tool_costs``,
            ``max_velocity``, ``expires_in``, ``allowed_services``).

    Returns:
        :class:`BudgetGuard` instance for inspecting spend and audit trail.
    """
    guard = BudgetGuard(
        budget=budget,
        max_per_call=max_per_call,
        demo=demo,
        agent_budgets=agent_budgets,
        **kwargs,
    )
    guard._enter_standalone()

    # Lazy import — crewai is an optional dependency.
    from crewai.hooks import (
        register_after_tool_call_hook,
        register_before_tool_call_hook,
    )

    def _before_tool(context) -> None:
        """Check budget before every tool call."""
        try:
            tool_name = getattr(context, "tool_name", "unknown")
            tool_input = getattr(context, "tool_input", {})
            agent_name = getattr(context, "agent_name", None) or getattr(context, "agent", None)
            if isinstance(tool_input, str):
                tool_input = {"input": tool_input}
            guard.before_tool(tool_name, tool_input, agent_name=agent_name)
        except BudgetExceededError as e:
            logger.warning("Budget exceeded, blocking tool %s: %s", tool_name, e)
            return False  # Block tool execution
        return None  # Allow execution

    def _after_tool(context) -> None:
        """Record cost after every tool call."""
        tool_name = getattr(context, "tool_name", "unknown")
        tool_result = str(getattr(context, "tool_result", ""))
        agent_name = getattr(context, "agent_name", None) or getattr(context, "agent", None)
        guard.after_tool(tool_name, tool_result, agent_name=agent_name)
        return None  # Keep original result

    register_before_tool_call_hook(_before_tool)
    register_after_tool_call_hook(_after_tool)

    return guard
