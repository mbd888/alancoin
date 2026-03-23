"""In-memory budget enforcement for zero-setup evaluation.

No server, no API key, no network calls. Tracks a Decimal balance and
deducts per tool call. Mirrors the DemoBackend pattern from mcp_proxy.py.
"""

import threading
from decimal import Decimal, InvalidOperation

from ..exceptions import AlancoinError


def _validate_amount(value: str, field_name: str, *, allow_zero: bool = False) -> Decimal:
    """Parse and validate a USDC amount string.

    Raises:
        ValueError: If the value is not a valid non-negative decimal.
    """
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field_name} must be a non-empty string, got {value!r}")
    try:
        d = Decimal(value)
    except InvalidOperation:
        raise ValueError(f"{field_name} must be a valid decimal, got {value!r}")
    if d < 0:
        raise ValueError(f"{field_name} must be non-negative, got {value!r}")
    if not allow_zero and d == 0:
        raise ValueError(f"{field_name} must be greater than zero, got {value!r}")
    return d


class BudgetExceededError(AlancoinError):
    """Raised when spending would exceed the budget or velocity limit."""

    def __init__(self, message: str):
        super().__init__(message=message, code="budget_exceeded", status_code=402)


class DemoGuard:
    """In-memory budget tracker for demo / offline evaluation.

    Args:
        budget: Total budget as a decimal string (e.g., ``"5.00"``).
        cost_per_call: Default cost per tool call (e.g., ``"0.01"``).
        tool_costs: Per-tool cost overrides: ``{"search": "0.05"}``.
    """

    def __init__(
        self,
        budget: str = "5.00",
        cost_per_call: str = "0.01",
        tool_costs: dict = None,
    ) -> None:
        self._budget = _validate_amount(budget, "budget")
        self._balance = self._budget
        self._cost_per_call = _validate_amount(cost_per_call, "cost_per_call", allow_zero=True)
        self._tool_costs = {}
        for k, v in (tool_costs or {}).items():
            self._tool_costs[k] = _validate_amount(v, f"tool_costs[{k!r}]", allow_zero=True)
        self._spent = Decimal("0")
        self._lock = threading.Lock()

    def charge(self, tool_name: str) -> str:
        """Deduct cost for *tool_name*. Returns the amount charged.

        Raises:
            BudgetExceededError: If the charge would exceed the remaining budget.
        """
        cost = self._tool_costs.get(tool_name, self._cost_per_call)
        with self._lock:
            if cost > self._balance:
                raise BudgetExceededError(
                    f"Tool '{tool_name}' costs ${cost} but only ${self._balance} remaining "
                    f"(budget: ${self._budget}, spent: ${self._spent})"
                )
            self._balance -= cost
            self._spent += cost
        return str(cost)

    def reserve(self, tool_name: str) -> str:
        """Reserve (pre-charge) the cost for *tool_name*.

        Same as :meth:`charge` — deducts from balance upfront.
        Use :meth:`refund` to roll back on failure.
        """
        return self.charge(tool_name)

    def peek_cost(self, tool_name: str) -> str:
        """Return what :meth:`charge` would deduct, without actually charging."""
        cost = self._tool_costs.get(tool_name, self._cost_per_call)
        return str(cost)

    def refund(self, amount: str) -> None:
        """Return *amount* to the balance (e.g., on tool failure)."""
        amt = Decimal(amount)
        with self._lock:
            self._balance += amt
            self._spent -= amt

    @property
    def total_spent(self) -> str:
        with self._lock:
            return str(self._spent)

    @property
    def remaining(self) -> str:
        with self._lock:
            return str(self._balance)

    @property
    def budget(self) -> str:
        return str(self._budget)
