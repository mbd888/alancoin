"""Minimal MCP server for testing the Alancoin payment proxy.

Tools:
    echo  — Returns text unchanged.
    calculate — Evaluates a simple math expression.

Usage:
    python examples/simple_mcp_server.py
"""

from mcp.server.fastmcp import FastMCP

mcp = FastMCP("simple-test-server")


@mcp.tool()
def echo(text: str) -> str:
    """Echo back the input text unchanged."""
    return text


@mcp.tool()
def calculate(expression: str) -> str:
    """Evaluate a simple math expression (e.g., '2 + 3 * 4').

    Only supports basic arithmetic: + - * / ** ( )
    """
    allowed = set("0123456789+-*/.() ")
    if not all(c in allowed for c in expression):
        raise ValueError(f"Invalid characters in expression: {expression}")
    result = eval(expression)  # noqa: S307 — input validated above
    return str(result)


if __name__ == "__main__":
    mcp.run(transport="stdio")
