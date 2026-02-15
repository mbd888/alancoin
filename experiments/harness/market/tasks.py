"""
Sequential task management for budget allocation experiments.

Tests multi-step economic planning: can agents reason about downstream
costs before making the first purchase?

Example: Total budget $0.40
  1. Summarize document ($0.10-0.15)
  2. Translate summary ($0.10-0.20)
  3. Process translation ($0.15-0.25)

If agent overspends on step 1, they fail at step 3.
"""

import threading
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional


class TaskStatus(str, Enum):
    """Status of a task in the sequence."""
    PENDING = "pending"
    IN_PROGRESS = "in_progress"
    COMPLETED = "completed"
    FAILED = "failed"
    BLOCKED = "blocked"  # Waiting on dependency


@dataclass
class SequentialTask:
    """A task that may depend on previous tasks."""

    task_id: str
    description: str
    service_type: str  # e.g., "inference", "translation"

    # Budget guidance (not hard limits - just for planning)
    estimated_cost_low: float = 0.0
    estimated_cost_high: float = 0.0

    # Dependencies
    depends_on: list[str] = field(default_factory=list)  # task_ids

    # State
    status: TaskStatus = TaskStatus.PENDING
    actual_cost: Optional[float] = None
    service_id: Optional[str] = None  # Service used to complete
    output: Optional[str] = None  # Output from previous step (input to next)


@dataclass
class TaskSequence:
    """
    A sequence of dependent tasks with a shared budget.

    The agent must plan across all tasks - overspending early
    means failing later.
    """

    sequence_id: str
    tasks: list[SequentialTask]
    total_budget: float

    # Tracking
    spent: float = 0.0
    completed_count: int = 0

    def __post_init__(self):
        self._lock = threading.Lock()

    def get_next_task(self) -> Optional[SequentialTask]:
        """Get the next task that can be started."""
        for task in self.tasks:
            if task.status == TaskStatus.PENDING:
                # Check if dependencies are met
                deps_met = all(
                    self._get_task(dep_id).status == TaskStatus.COMPLETED
                    for dep_id in task.depends_on
                )
                if deps_met:
                    return task
        return None

    def _get_task(self, task_id: str) -> Optional[SequentialTask]:
        """Get task by ID."""
        for task in self.tasks:
            if task.task_id == task_id:
                return task
        return None

    def start_task(self, task_id: str) -> bool:
        """Mark a task as in progress."""
        task = self._get_task(task_id)
        if task and task.status == TaskStatus.PENDING:
            task.status = TaskStatus.IN_PROGRESS
            return True
        return False

    def complete_task(self, task_id: str, cost: float, output: str = "") -> bool:
        """Mark a task as completed."""
        with self._lock:
            task = self._get_task(task_id)
            if task and task.status == TaskStatus.IN_PROGRESS:
                task.status = TaskStatus.COMPLETED
                task.actual_cost = cost
                task.output = output
                self.spent += cost
                self.completed_count += 1
                return True
            return False

    def fail_task(self, task_id: str, reason: str = "") -> None:
        """Mark a task as failed."""
        task = self._get_task(task_id)
        if task:
            task.status = TaskStatus.FAILED
            task.output = reason
            # Mark dependent tasks as blocked
            for other in self.tasks:
                if task_id in other.depends_on:
                    other.status = TaskStatus.BLOCKED

    @property
    def remaining_budget(self) -> float:
        """Budget remaining for future tasks."""
        return self.total_budget - self.spent

    @property
    def is_complete(self) -> bool:
        """Whether all tasks are completed."""
        return all(t.status == TaskStatus.COMPLETED for t in self.tasks)

    @property
    def is_failed(self) -> bool:
        """Whether any task failed."""
        return any(t.status in (TaskStatus.FAILED, TaskStatus.BLOCKED) for t in self.tasks)

    @property
    def completion_rate(self) -> float:
        """Fraction of tasks completed."""
        return self.completed_count / len(self.tasks) if self.tasks else 0.0

    def get_planning_context(self) -> str:
        """
        Generate context string for the agent to reason about budget allocation.

        This is what makes sequential allocation interesting - the agent
        sees the full pipeline and must plan accordingly.
        """
        lines = [
            f"**Task Sequence** (Total Budget: ${self.total_budget:.2f})",
            f"Remaining Budget: ${self.remaining_budget:.2f}",
            "",
        ]

        for i, task in enumerate(self.tasks, 1):
            status_icon = {
                TaskStatus.PENDING: "⬜",
                TaskStatus.IN_PROGRESS: "🔄",
                TaskStatus.COMPLETED: "✅",
                TaskStatus.FAILED: "❌",
                TaskStatus.BLOCKED: "🚫",
            }.get(task.status, "?")

            cost_range = f"${task.estimated_cost_low:.2f}-${task.estimated_cost_high:.2f}"

            line = f"{i}. {status_icon} {task.description}"
            line += f" ({task.service_type}, est. {cost_range})"

            if task.depends_on:
                deps = ", ".join(task.depends_on)
                line += f" [requires: {deps}]"

            if task.status == TaskStatus.COMPLETED:
                line += f" - DONE (${task.actual_cost:.2f})"

            lines.append(line)

        # Add planning hint
        pending_costs = sum(
            (t.estimated_cost_low + t.estimated_cost_high) / 2
            for t in self.tasks
            if t.status == TaskStatus.PENDING
        )

        if pending_costs > 0:
            lines.append("")
            lines.append(f"Estimated cost for remaining tasks: ~${pending_costs:.2f}")
            if pending_costs > self.remaining_budget:
                lines.append("⚠️ WARNING: Estimated costs exceed remaining budget!")

        return "\n".join(lines)


# Pre-defined task sequences for experiments

def create_document_processing_sequence(
    total_budget: float = 0.40,
    sequence_id: str = "doc_process",
) -> TaskSequence:
    """
    Standard document processing pipeline.

    Summarize → Translate → Process

    Tight budget forces planning tradeoffs.
    """
    return TaskSequence(
        sequence_id=sequence_id,
        total_budget=total_budget,
        tasks=[
            SequentialTask(
                task_id="summarize",
                description="Summarize the source document",
                service_type="inference",
                estimated_cost_low=0.08,
                estimated_cost_high=0.15,
                depends_on=[],
            ),
            SequentialTask(
                task_id="translate",
                description="Translate the summary to Spanish",
                service_type="translation",
                estimated_cost_low=0.10,
                estimated_cost_high=0.18,
                depends_on=["summarize"],
            ),
            SequentialTask(
                task_id="process",
                description="Extract key entities from translation",
                service_type="inference",
                estimated_cost_low=0.12,
                estimated_cost_high=0.20,
                depends_on=["translate"],
            ),
        ],
    )


def create_analysis_sequence(
    total_budget: float = 0.60,
    sequence_id: str = "analysis",
) -> TaskSequence:
    """
    Data analysis pipeline with parallel-then-merge structure.

    Analyze A ─┐
               ├─→ Merge
    Analyze B ─┘

    Tests whether agents can handle more complex dependency graphs.
    """
    return TaskSequence(
        sequence_id=sequence_id,
        total_budget=total_budget,
        tasks=[
            SequentialTask(
                task_id="analyze_a",
                description="Analyze dataset A",
                service_type="inference",
                estimated_cost_low=0.10,
                estimated_cost_high=0.20,
                depends_on=[],
            ),
            SequentialTask(
                task_id="analyze_b",
                description="Analyze dataset B",
                service_type="inference",
                estimated_cost_low=0.10,
                estimated_cost_high=0.20,
                depends_on=[],
            ),
            SequentialTask(
                task_id="merge",
                description="Merge and synthesize both analyses",
                service_type="inference",
                estimated_cost_low=0.15,
                estimated_cost_high=0.25,
                depends_on=["analyze_a", "analyze_b"],
            ),
        ],
    )
