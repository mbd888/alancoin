"""LangGraph budget enforcement via callback handler.

Intercepts every tool call in a LangGraph graph and enforces Alancoin
budget limits. Zero changes to the graph definition required.

Usage (3 lines)::

    from alancoin.agents.langgraph import budget_handler

    handler = budget_handler(budget="5.00", demo=True)
    result = graph.invoke(input, config={"callbacks": [handler]})

After execution::

    print(handler.guard.total_spent)       # "$1.23"
    print(handler.guard.cost_report())     # per-tool breakdown
    print(handler.guard.audit_trail.to_json())  # EU AI Act export
    handler.close()  # Release resources

Requires: ``pip install alancoin[langgraph]``
"""

import logging
import threading
from typing import Any, Dict, Optional
from uuid import UUID

from ._guard import BudgetGuard

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Lazy import to avoid hard dependency on langchain-core at import time.
# ---------------------------------------------------------------------------

_BaseCallbackHandler = None


def _get_base_handler():
    global _BaseCallbackHandler
    if _BaseCallbackHandler is None:
        from langchain_core.callbacks.base import BaseCallbackHandler

        _BaseCallbackHandler = BaseCallbackHandler
    return _BaseCallbackHandler


# ---------------------------------------------------------------------------
# Handler class factory — built once, inherits from BaseCallbackHandler.
# ---------------------------------------------------------------------------

_HANDLER_CLASS = None


def _build_handler_class():
    global _HANDLER_CLASS
    if _HANDLER_CLASS is not None:
        return _HANDLER_CLASS

    base = _get_base_handler()

    class AlancoinBudgetHandler(base):
        """LangGraph callback handler that enforces budget on every tool call.

        Pass to ``graph.invoke(input, config={"callbacks": [handler]})``.

        Attributes:
            guard: The underlying :class:`BudgetGuard` for cost inspection.
        """

        def __init__(self, guard: BudgetGuard) -> None:
            self.guard = guard
            self._active_tools: Dict[UUID, str] = {}
            self._lock = threading.Lock()

        # -- Resource management ----------------------------------------------

        def close(self) -> None:
            """Close the underlying BudgetGuard and release resources."""
            self.guard.close()

        def __enter__(self) -> "AlancoinBudgetHandler":
            return self

        def __exit__(self, exc_type, exc_val, exc_tb) -> bool:
            self.close()
            return False

        # -- Callback hooks ---------------------------------------------------

        def on_tool_start(
            self,
            serialized: Dict[str, Any],
            input_str: str,
            *,
            run_id: UUID,
            parent_run_id: Optional[UUID] = None,
            tags: Optional[list] = None,
            metadata: Optional[dict] = None,
            **kwargs: Any,
        ) -> None:
            tool_name = serialized.get("name", "unknown")
            with self._lock:
                self._active_tools[run_id] = tool_name
            logger.debug("Tool started: %s (run_id=%s)", tool_name, run_id)
            self.guard.before_tool(tool_name, {"input": input_str})

        def on_tool_end(
            self,
            output: Any,
            *,
            run_id: UUID,
            parent_run_id: Optional[UUID] = None,
            tags: Optional[list] = None,
            **kwargs: Any,
        ) -> None:
            with self._lock:
                tool_name = self._active_tools.pop(run_id, "unknown")
            output_str = str(output)

            # Extract actual cost from gateway metadata if present.
            actual_cost = None
            if isinstance(output, dict):
                gw = output.get("_gateway")
                if isinstance(gw, dict) and gw.get("amountPaid"):
                    actual_cost = gw["amountPaid"]

            logger.debug("Tool completed: %s (run_id=%s, cost=%s)", tool_name, run_id, actual_cost or "demo")
            self.guard.after_tool(tool_name, output_str, actual_cost=actual_cost)

        def on_tool_error(
            self,
            error: BaseException,
            *,
            run_id: UUID,
            parent_run_id: Optional[UUID] = None,
            tags: Optional[list] = None,
            **kwargs: Any,
        ) -> None:
            with self._lock:
                tool_name = self._active_tools.pop(run_id, "unknown")
            logger.warning("Tool failed: %s (run_id=%s): %s", tool_name, run_id, error)
            self.guard.on_tool_error(tool_name, error)

    _HANDLER_CLASS = AlancoinBudgetHandler
    return _HANDLER_CLASS


# ---------------------------------------------------------------------------
# Module-level __getattr__ for lazy import of AlancoinBudgetHandler.
# ---------------------------------------------------------------------------


def __getattr__(name: str):
    if name == "AlancoinBudgetHandler":
        return _build_handler_class()
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


# ---------------------------------------------------------------------------
# Factory function.
# ---------------------------------------------------------------------------


def budget_handler(
    budget: str = "5.00",
    max_per_call: str = None,
    demo: bool = False,
    **kwargs: Any,
):
    """Create a LangGraph budget enforcement handler.

    Example::

        from alancoin.agents.langgraph import budget_handler

        handler = budget_handler(budget="5.00", demo=True)
        result = graph.invoke(input, config={"callbacks": [handler]})
        handler.close()  # or use: with budget_handler(...) as handler:

    Args:
        budget: Total USDC budget for this workflow.
        max_per_call: Max USDC per tool call.
        demo: If True, use in-memory budget (no server needed).
        **kwargs: Forwarded to :class:`BudgetGuard`
            (``url``, ``api_key``, ``cost_per_call``, ``tool_costs``,
            ``max_velocity``, ``expires_in``, ``allowed_services``).

    Returns:
        A callback handler to pass via ``config={"callbacks": [handler]}``.
        Call ``.close()`` when done, or use as a context manager.
    """
    cls = _build_handler_class()
    guard = BudgetGuard(
        budget=budget,
        max_per_call=max_per_call,
        demo=demo,
        **kwargs,
    )
    guard._enter_standalone()
    logger.info("Created budget handler (budget=%s, demo=%s)", budget, demo)
    return cls(guard)
