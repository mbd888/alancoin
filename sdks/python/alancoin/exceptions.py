"""Custom exceptions for Alancoin SDK."""


class AlancoinError(Exception):
    """Base exception for Alancoin errors."""

    def __init__(self, message: str, code: str = None, status_code: int = None, details: dict = None):
        super().__init__(message)
        self.message = message
        self.code = code
        self.status_code = status_code
        self.details = details or {}

    @property
    def funds_status(self) -> str:
        """Funds safety status from server (e.g., 'no_change', 'held_pending')."""
        return self.details.get("funds_status", "")

    @property
    def recovery(self) -> str:
        """Recovery guidance from server."""
        return self.details.get("recovery", "")

    def __str__(self):
        if self.code:
            return f"[{self.code}] {self.message}"
        return self.message


class AgentNotFoundError(AlancoinError):
    """Agent not found in registry."""
    
    def __init__(self, address: str):
        super().__init__(
            message=f"Agent not found: {address}",
            code="agent_not_found",
            status_code=404,
        )
        self.address = address


class AgentExistsError(AlancoinError):
    """Agent already registered."""
    
    def __init__(self, address: str):
        super().__init__(
            message=f"Agent already exists: {address}",
            code="agent_exists",
            status_code=409,
        )
        self.address = address


class ServiceNotFoundError(AlancoinError):
    """Service not found."""
    
    def __init__(self, service_id: str):
        super().__init__(
            message=f"Service not found: {service_id}",
            code="service_not_found",
            status_code=404,
        )
        self.service_id = service_id


class PaymentError(AlancoinError):
    """Payment failed."""
    
    def __init__(self, message: str, tx_hash: str = None):
        super().__init__(
            message=message,
            code="payment_failed",
            status_code=402,
        )
        self.tx_hash = tx_hash


class ValidationError(AlancoinError):
    """Invalid request data."""
    
    def __init__(self, message: str, field: str = None):
        super().__init__(
            message=message,
            code="validation_error",
            status_code=400,
        )
        self.field = field


class PaymentRequiredError(AlancoinError):
    """Payment required to access resource (HTTP 402)."""
    
    def __init__(
        self,
        price: str,
        recipient: str,
        currency: str = "USDC",
        chain: str = "base-sepolia",
        contract: str = None,
    ):
        super().__init__(
            message=f"Payment required: {price} {currency}",
            code="payment_required",
            status_code=402,
        )
        self.price = price
        self.recipient = recipient
        self.currency = currency
        self.chain = chain
        self.contract = contract


class NetworkError(AlancoinError):
    """Network/connection error."""
    
    def __init__(self, message: str, original_error: Exception = None):
        super().__init__(
            message=message,
            code="network_error",
            status_code=None,
        )
        self.original_error = original_error
