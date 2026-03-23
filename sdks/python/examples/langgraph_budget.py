"""Budget-enforced LangGraph agent in 3 lines.

pip install alancoin[langgraph]

This example uses demo mode (no server needed).
For production, replace demo=True with url="..." and api_key="...".
"""

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent

# --- Your existing tools (unchanged) ---


@tool
def search(query: str) -> str:
    """Search the web for information."""
    return f"Results for: {query}"


@tool
def calculator(expression: str) -> str:
    """Calculate a math expression."""
    return str(eval(expression))  # noqa: S307


# --- Your existing agent (unchanged) ---

model = ChatOpenAI(model="gpt-4o")
agent = create_react_agent(model, [search, calculator])

# === ADD BUDGET ENFORCEMENT (3 lines) ===
from alancoin.agents.langgraph import budget_handler

handler = budget_handler(budget="5.00", demo=True, cost_per_call="0.10")
result = agent.invoke(
    {"messages": [("user", "What is the population of France times 2?")]},
    config={"callbacks": [handler]},
)
# =========================================

# Inspect costs
print(f"Total spent: ${handler.guard.total_spent}")
print(f"Remaining:   ${handler.guard.remaining}")
print(f"Tool calls:  {handler.guard.call_count}")
print()

# Per-tool breakdown
report = handler.guard.cost_report()
for tool_name, cost in report["by_tool"].items():
    print(f"  {tool_name}: ${cost}")
print()

# Export audit trail (EU AI Act compliance)
print(handler.guard.audit_trail.to_json())
