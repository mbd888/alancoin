"""Budget-enforced CrewAI crew in 3 lines.

pip install alancoin[crewai]

This example uses demo mode (no server needed).
For production, replace demo=True with url="..." and api_key="...".
"""

from crewai import Agent, Crew, Task
from crewai_tools import SerperDevTool

# --- Your existing crew (unchanged) ---

researcher = Agent(
    role="Senior Researcher",
    goal="Find the latest information on AI agent frameworks",
    backstory="You are an expert researcher.",
    tools=[SerperDevTool()],
    verbose=True,
)

task = Task(
    description="Research the top 3 AI agent frameworks in 2026 and summarize their strengths.",
    expected_output="A concise comparison of the top 3 frameworks.",
    agent=researcher,
)

crew = Crew(agents=[researcher], tasks=[task], verbose=True)

# === ADD BUDGET ENFORCEMENT (3 lines) ===
from alancoin.agents.crewai import enable_budget

guard = enable_budget(budget="10.00", demo=True, cost_per_call="0.25")
result = crew.kickoff()
# =========================================

# Inspect costs
print(f"\nTotal spent: ${guard.total_spent}")
print(f"Remaining:   ${guard.remaining}")
print(f"Tool calls:  {guard.call_count}")
print()

# Per-tool breakdown
report = guard.cost_report()
for tool_name, cost in report["by_tool"].items():
    print(f"  {tool_name}: ${cost}")
print()

# Export audit trail (EU AI Act compliance)
print(guard.audit_trail.to_json())
