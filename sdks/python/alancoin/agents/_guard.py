"""BudgetGuard — the core budget enforcement wrapper.

Wraps either a live GatewaySession (server mode) or a DemoGuard
(demo mode) and adds:

- Per-tool-call cost tracking with attribution
- Client-side velocity circuit breaker
- Hash-chained audit log for compliance
"""

import logging
import threading
import time
from collections import defaultdict
from decimal import Decimal
from typing import Dict, List, Optional

from ._audit import (
    ACTION_BUDGET_EXCEEDED,
    ACTION_TOOL_COMPLETED,
    ACTION_TOOL_FAILED,
    ACTION_TOOL_STARTED,
    AuditLog,
)
from ._demo import BudgetExceededError, DemoGuard, _validate_amount

logger = logging.getLogger(__name__)


class BudgetGuard:
    """Budget enforcement for AI agent tool calls.

    In **demo mode** (``demo=True``), runs entirely in-memory with flat
    per-tool costs. No server, no API key needed.

    In **server mode**, wraps an Alancoin ``GatewaySession``. The server
    enforces budget holds; the guard adds audit logging and a client-side
    velocity circuit breaker.

    Example (demo)::

        with BudgetGuard(budget="5.00", demo=True) as guard:
            guard.before_tool("search", {"query": "AI agents"})
            # ... tool executes ...
            guard.after_tool("search", "results here")

    Args:
        url: Alancoin server URL (server mode only).
        api_key: API key (server mode only).
        budget: Total USDC budget (e.g., ``"5.00"``).
        max_per_call: Max USDC per tool call. Defaults to *budget*.
        expires_in: Session duration (e.g., ``"1h"``).
        allowed_services: Restrict to these service types.
        cost_per_call: Default cost per tool call in demo mode.
        tool_costs: Per-tool cost overrides: ``{"search": "0.05"}``.
        max_velocity: Max USDC/minute before circuit breaker trips.
        demo: If True, use in-memory budget (no server needed).
    """

    def __init__(
        self,
        url: str = None,
        api_key: str = None,
        budget: str = "5.00",
        max_per_call: str = None,
        expires_in: str = "1h",
        allowed_services: list = None,
        cost_per_call: str = "0.01",
        tool_costs: dict = None,
        max_velocity: float = None,
        demo: bool = False,
        on_budget_warning: callable = None,
        warning_thresholds: tuple = (0.75, 0.90),
        audit_path: str = None,
        agent_budgets: dict = None,
    ) -> None:
        # Validate at construction time
        _validate_amount(budget, "budget")
        if max_per_call is not None:
            _validate_amount(max_per_call, "max_per_call")
        _validate_amount(cost_per_call, "cost_per_call", allow_zero=True)
        if tool_costs:
            for k, v in tool_costs.items():
                _validate_amount(v, f"tool_costs[{k!r}]", allow_zero=True)

        self._url = url
        self._api_key = api_key
        self._budget = budget
        self._max_per_call = max_per_call
        self._expires_in = expires_in
        self._allowed_services = allowed_services
        self._cost_per_call = cost_per_call
        self._tool_costs = tool_costs
        self._max_velocity = max_velocity
        self._demo = demo

        self._audit = AuditLog(audit_path=audit_path)
        self._lock = threading.Lock()
        self._call_count = 0
        self._total_spent = Decimal("0")
        self._tool_totals: Dict[str, Decimal] = defaultdict(Decimal)
        self._pending_reserves: Dict[str, List[str]] = defaultdict(list)
        self._on_budget_warning = on_budget_warning
        self._warning_thresholds = sorted(warning_thresholds)
        self._fired_thresholds: set = set()
        self._agent_budgets = agent_budgets
        self._agent_spent: Dict[str, Decimal] = defaultdict(Decimal)
        self._active = False
        self._entered_standalone = False

        # Backends (initialised in __enter__)
        self._demo_guard: Optional[DemoGuard] = None
        self._gateway = None  # GatewaySession (server mode)
        self._client = None  # Alancoin client (server mode)

    # -- Context manager ------------------------------------------------------

    def __enter__(self) -> "BudgetGuard":
        if self._demo:
            self._demo_guard = DemoGuard(
                budget=self._budget,
                cost_per_call=self._cost_per_call,
                tool_costs=self._tool_costs,
            )
        else:
            from ..client import Alancoin

            self._client = Alancoin(
                base_url=self._url,
                api_key=self._api_key,
            )
            self._gateway = self._client.gateway(
                max_total=self._budget,
                max_per_tx=self._max_per_call,
                expires_in=self._expires_in,
                allowed_services=self._allowed_services,
            )
            self._gateway.__enter__()

        self._active = True
        logger.info(
            "BudgetGuard opened (mode=%s, budget=%s)",
            "demo" if self._demo else "server",
            self._budget,
        )
        return self

    def _enter_standalone(self) -> "BudgetGuard":
        """Enter the guard outside a ``with`` block.

        Use :meth:`close` to release resources when done.
        """
        self.__enter__()
        self._entered_standalone = True
        return self

    def close(self) -> None:
        """Explicitly close the guard and release resources.

        Safe to call multiple times. Equivalent to exiting the context manager.
        """
        if self._active:
            self.__exit__(None, None, None)

    def __del__(self) -> None:
        if getattr(self, "_active", False) and getattr(self, "_entered_standalone", False):
            logger.warning(
                "BudgetGuard was never closed. Call .close() or use `with` to avoid resource leaks."
            )
            try:
                self.close()
            except Exception:
                pass

    def __exit__(self, exc_type, exc_val, exc_tb) -> bool:
        self._active = False
        if self._gateway is not None:
            try:
                self._gateway.__exit__(exc_type, exc_val, exc_tb)
            except Exception as e:
                logger.warning("Failed to close gateway session: %s", e)
            self._gateway = None
        if self._client is not None:
            try:
                self._client.close()
            except Exception:
                pass
            self._client = None
        self._demo_guard = None
        return False

    # -- Tool lifecycle -------------------------------------------------------

    def before_tool(self, tool_name: str, tool_input: dict, agent_name: str = None) -> None:
        """Reserve budget before a tool call. Raises on overspend.

        In demo mode, the cost is deducted upfront (pre-charged). If the
        tool fails, :meth:`on_tool_error` refunds the reservation.

        Args:
            tool_name: Name of the tool being invoked.
            tool_input: Tool input parameters (logged in audit trail).
            agent_name: Optional agent name for per-agent budget tracking.

        Raises:
            BudgetExceededError: If budget is exhausted, velocity limit hit,
                or per-call limit exceeded.
        """
        if not self._active:
            raise BudgetExceededError("BudgetGuard is not active")

        self._check_velocity()
        self._check_max_per_call(tool_name)

        # Per-agent budget check
        if agent_name and self._agent_budgets and agent_name in self._agent_budgets:
            agent_budget = Decimal(self._agent_budgets[agent_name])
            with self._lock:
                if self._agent_spent[agent_name] >= agent_budget:
                    raise BudgetExceededError(
                        f"Agent '{agent_name}' exhausted its budget of ${agent_budget}"
                    )

        # Pre-charge in demo mode (reserve funds before tool runs).
        reserved = "0"
        if self._demo and self._demo_guard is not None:
            reserved = self._demo_guard.reserve(tool_name)

        with self._lock:
            reserved_dec = Decimal(reserved)
            if reserved_dec > 0:
                self._total_spent += reserved_dec
                self._tool_totals[tool_name] += reserved_dec
            self._pending_reserves[tool_name].append(reserved)

        self._audit.append(
            action=ACTION_TOOL_STARTED,
            tool_name=tool_name,
            cost=reserved,
            budget_remaining=self._get_remaining(),
            input_summary=str(tool_input),
        )

    def after_tool(
        self, tool_name: str, result: str, actual_cost: str = None, agent_name: str = None
    ) -> None:
        """Confirm a tool call and finalize its cost.

        In demo mode the cost was already reserved in :meth:`before_tool`.
        In server mode, *actual_cost* adjusts the running total.

        Args:
            tool_name: Name of the tool that completed.
            result: Tool output (truncated in audit log).
            actual_cost: Actual USDC charged (server mode). Optional.
        """
        with self._lock:
            reserved = (
                self._pending_reserves[tool_name].pop(0)
                if self._pending_reserves[tool_name]
                else "0"
            )
            self._call_count += 1

        # In server mode with actual_cost, adjust the difference from reserved.
        final_cost = reserved
        if actual_cost is not None:
            final_cost = actual_cost
            diff = Decimal(actual_cost) - Decimal(reserved)
            with self._lock:
                self._total_spent += diff
                self._tool_totals[tool_name] += diff

        # Server-mode max_per_call check (cost only known after execution).
        if self._max_per_call is not None and actual_cost is not None:
            if Decimal(final_cost) > Decimal(self._max_per_call):
                raise BudgetExceededError(
                    f"Tool '{tool_name}' cost ${final_cost} exceeds "
                    f"max_per_call limit of ${self._max_per_call}"
                )

        # Track per-agent spend
        if agent_name and final_cost != "0":
            with self._lock:
                self._agent_spent[agent_name] += Decimal(final_cost)

        self._audit.append(
            action=ACTION_TOOL_COMPLETED,
            tool_name=tool_name,
            cost=final_cost,
            budget_remaining=self._get_remaining(),
            output_summary=str(result),
        )

        self._check_warnings()

    def on_tool_error(self, tool_name: str, error: Exception) -> None:
        """Refund a pre-charged reservation on tool failure.

        Args:
            tool_name: Name of the tool that failed.
            error: The exception that occurred.
        """
        with self._lock:
            reserved = (
                self._pending_reserves[tool_name].pop(0)
                if self._pending_reserves[tool_name]
                else "0"
            )

        reserved_dec = Decimal(reserved)
        if reserved_dec > 0:
            if self._demo and self._demo_guard is not None:
                self._demo_guard.refund(reserved)
            with self._lock:
                self._total_spent -= reserved_dec
                self._tool_totals[tool_name] -= reserved_dec

        self._audit.append(
            action=ACTION_TOOL_FAILED,
            tool_name=tool_name,
            cost="0",
            budget_remaining=self._get_remaining(),
            output_summary=str(error)[:200],
        )

    # -- Properties -----------------------------------------------------------

    @property
    def total_spent(self) -> str:
        """Total USDC spent across all tool calls."""
        with self._lock:
            return str(self._total_spent)

    @property
    def remaining(self) -> str:
        """USDC remaining in the budget."""
        with self._lock:
            return str(Decimal(self._budget) - self._total_spent)

    @property
    def call_count(self) -> int:
        """Number of completed tool calls."""
        with self._lock:
            return self._call_count

    @property
    def is_over_budget(self) -> bool:
        """True if remaining budget is zero or negative."""
        with self._lock:
            return self._total_spent >= Decimal(self._budget)

    @property
    def audit_trail(self) -> AuditLog:
        """The hash-chained audit log."""
        return self._audit

    def cost_report(self) -> dict:
        """Per-tool cost attribution breakdown.

        Returns:
            Dict with ``total_spent``, ``remaining``, ``call_count``,
            ``budget``, and ``by_tool`` (per-tool totals).
        """
        with self._lock:
            by_tool = {k: str(v) for k, v in self._tool_totals.items()}
        return {
            "total_spent": self.total_spent,
            "remaining": self.remaining,
            "call_count": self.call_count,
            "budget": self._budget,
            "by_tool": by_tool,
        }

    # -- Internal -------------------------------------------------------------

    def _get_remaining(self) -> str:
        with self._lock:
            return str(Decimal(self._budget) - self._total_spent)

    def _check_warnings(self) -> None:
        """Fire budget warning callback when spend crosses a threshold."""
        if self._on_budget_warning is None:
            return
        budget_dec = Decimal(self._budget)
        if budget_dec == 0:
            return
        with self._lock:
            ratio = float(self._total_spent / budget_dec)
        for threshold in self._warning_thresholds:
            if threshold not in self._fired_thresholds and ratio >= threshold:
                self._fired_thresholds.add(threshold)
                try:
                    self._on_budget_warning(threshold, self.total_spent, self.remaining)
                except Exception as e:
                    logger.warning("Budget warning callback failed: %s", e)

    def _check_max_per_call(self, tool_name: str) -> None:
        """Raise if the tool's cost would exceed max_per_call."""
        if self._max_per_call is None:
            return
        max_dec = Decimal(self._max_per_call)
        # In demo mode, cost is predictable — check before execution.
        if self._demo and self._demo_guard is not None:
            preview = Decimal(self._demo_guard.peek_cost(tool_name))
            if preview > max_dec:
                self._audit.append(
                    action=ACTION_BUDGET_EXCEEDED,
                    tool_name=tool_name,
                    cost="0",
                    budget_remaining=self._get_remaining(),
                    output_summary=f"Cost ${preview} exceeds max_per_call ${self._max_per_call}",
                )
                raise BudgetExceededError(
                    f"Tool '{tool_name}' cost ${preview} exceeds "
                    f"max_per_call limit of ${self._max_per_call}"
                )

    def _check_velocity(self) -> None:
        """Raise if spend velocity exceeds max_velocity USDC/minute."""
        if self._max_velocity is None:
            return

        cutoff = time.time() - 60.0
        recent_spend = Decimal("0")
        for entry in self._audit:
            if entry.timestamp >= cutoff and entry.action == ACTION_TOOL_COMPLETED:
                recent_spend += Decimal(entry.cost)

        if float(recent_spend) >= self._max_velocity:
            self._audit.append(
                action=ACTION_BUDGET_EXCEEDED,
                tool_name="",
                cost="0",
                budget_remaining=self._get_remaining(),
                output_summary=f"Velocity {recent_spend}/min exceeds {self._max_velocity}/min",
            )
            raise BudgetExceededError(
                f"Velocity circuit breaker: ${recent_spend}/min exceeds "
                f"${self._max_velocity}/min limit"
            )
