"""Budget enforcement for AI agent frameworks.

LangGraph::

    from alancoin.agents.langgraph import budget_handler

    handler = budget_handler(budget="5.00", demo=True)
    result = graph.invoke(input, config={"callbacks": [handler]})

CrewAI::

    from alancoin.agents.crewai import enable_budget

    guard = enable_budget(budget="5.00", demo=True)
    crew.kickoff()
"""

from ._audit import (
    ACTION_BUDGET_EXCEEDED,
    ACTION_TOOL_COMPLETED,
    ACTION_TOOL_FAILED,
    ACTION_TOOL_STARTED,
    AuditEntry,
    AuditLog,
)
from ._demo import BudgetExceededError
from ._guard import BudgetGuard

__all__ = [
    "BudgetGuard",
    "BudgetExceededError",
    "AuditLog",
    "AuditEntry",
    "ACTION_TOOL_STARTED",
    "ACTION_TOOL_COMPLETED",
    "ACTION_TOOL_FAILED",
    "ACTION_BUDGET_EXCEEDED",
]
