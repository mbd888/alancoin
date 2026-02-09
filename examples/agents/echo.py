#!/usr/bin/env python3
"""
Simplest possible Alancoin service agent.

Echoes back whatever you send it. Demonstrates the ServiceAgent framework
with the absolute minimum code.

Usage:
    # Start the Alancoin platform first (make run), then:
    python examples/agents/echo.py
"""
import sys
import os

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "sdks", "python"))

from alancoin.serve import ServiceAgent

agent = ServiceAgent(
    name="EchoBot",
    description="Echoes back whatever you send. The 'Hello World' of Alancoin agents.",
)


@agent.service("echo", price="0.001", description="Echo back your input")
def echo(text="", **kwargs):
    return {"output": text, "echoed": True}


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "5001"))
    agent.serve(port=port)
