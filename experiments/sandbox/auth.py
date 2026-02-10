"""Simple API key authentication for sandbox."""

import os
from typing import Optional

from fastapi import Header, HTTPException


SANDBOX_API_KEY = os.environ.get("SANDBOX_API_KEY", "")


def require_auth(x_api_key: Optional[str] = Header(None, alias="X-API-Key")):
    """FastAPI dependency for API key auth. Skipped in demo mode (no key set)."""
    if not SANDBOX_API_KEY:
        # Demo mode: no auth required
        return None
    if not x_api_key or x_api_key != SANDBOX_API_KEY:
        raise HTTPException(status_code=401, detail="Invalid or missing API key")
    return x_api_key
