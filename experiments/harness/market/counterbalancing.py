"""
Counterbalancing utilities for experiment design.

Implements Latin square designs to ensure each model plays each role
equally across experiment runs, controlling for order effects.
"""

from dataclasses import dataclass
from typing import Optional
import itertools


@dataclass
class Assignment:
    """A model-role assignment for an experiment run."""

    model: str
    role: str
    condition: str
    run_id: int


class LatinSquare:
    """
    Generate Latin square designs for counterbalancing.

    A Latin square ensures that each treatment (model) appears exactly once
    in each row (run block) and each column (role/condition).
    """

    def __init__(self, elements: list[str]):
        """
        Initialize Latin square generator.

        Args:
            elements: List of elements to arrange (e.g., model names)
        """
        self.elements = elements
        self.n = len(elements)

    def generate(self) -> list[list[str]]:
        """
        Generate a standard Latin square.

        Returns:
            n×n grid where each element appears once per row and column
        """
        square = []
        for i in range(self.n):
            row = []
            for j in range(self.n):
                idx = (i + j) % self.n
                row.append(self.elements[idx])
            square.append(row)
        return square

    def generate_balanced(self) -> list[list[str]]:
        """
        Generate a balanced Latin square (Williams design).

        Balanced squares also control for first-order carryover effects.

        Returns:
            n×n grid with balanced ordering
        """
        if self.n == 0:
            return []

        if self.n == 1:
            return [[self.elements[0]]]

        if self.n == 2:
            return [
                [self.elements[0], self.elements[1]],
                [self.elements[1], self.elements[0]],
            ]

        # Williams design for n > 2
        square = []
        for i in range(self.n):
            row = [None] * self.n
            for j in range(self.n):
                if j % 2 == 0:
                    idx = (i + j // 2) % self.n
                else:
                    idx = (self.n - 1 - (i + j // 2) % self.n) % self.n
                row[j] = self.elements[idx]
            square.append(row)

        return square


def generate_counterbalanced_assignments(
    models: list[str],
    roles: list[str],
    conditions: list[str],
    runs_per_condition: int,
) -> list[Assignment]:
    """
    Generate counterbalanced model-role-condition assignments.

    Ensures each model plays each role equally across conditions.

    Args:
        models: List of model names
        roles: List of roles (e.g., ["buyer", "seller"])
        conditions: List of experimental conditions
        runs_per_condition: Number of runs per condition

    Returns:
        List of Assignment objects
    """
    assignments = []
    run_id = 0

    # For each condition, create a counterbalanced set of runs
    for condition in conditions:
        # Generate Latin square for model-role assignment
        latin = LatinSquare(models)
        square = latin.generate_balanced()

        # Repeat the square to get enough runs
        full_runs = runs_per_condition
        repeats_needed = (full_runs + len(models) - 1) // len(models)

        for repeat in range(repeats_needed):
            for row_idx, row in enumerate(square):
                if run_id >= len(conditions) * runs_per_condition:
                    break

                # Assign models to roles for this run
                for role_idx, model in enumerate(row):
                    if role_idx < len(roles):
                        assignment = Assignment(
                            model=model,
                            role=roles[role_idx],
                            condition=condition,
                            run_id=run_id,
                        )
                        assignments.append(assignment)

                run_id += 1

    return assignments


def generate_factorial_design(
    factors: dict[str, list[str]],
    runs_per_cell: int,
) -> list[dict]:
    """
    Generate a full factorial design.

    Args:
        factors: Dictionary of factor names to levels
        runs_per_cell: Number of runs per factor combination

    Returns:
        List of condition dictionaries
    """
    factor_names = list(factors.keys())
    factor_levels = [factors[name] for name in factor_names]

    conditions = []

    # Generate all combinations
    for combination in itertools.product(*factor_levels):
        cell = dict(zip(factor_names, combination))

        for run in range(runs_per_cell):
            condition = cell.copy()
            condition["run"] = run
            conditions.append(condition)

    return conditions


def generate_model_rotation(
    models: list[str],
    num_runs: int,
) -> list[str]:
    """
    Generate a model rotation for sequential runs.

    Ensures equal distribution of models across runs.

    Args:
        models: List of model names
        num_runs: Total number of runs

    Returns:
        List of model names, one per run
    """
    rotation = []
    for i in range(num_runs):
        rotation.append(models[i % len(models)])
    return rotation


def validate_balance(
    assignments: list[Assignment],
    models: list[str],
    roles: list[str],
) -> dict:
    """
    Validate that assignments are properly balanced.

    Returns:
        Dictionary with balance statistics
    """
    # Count model-role combinations
    counts = {}
    for model in models:
        for role in roles:
            key = (model, role)
            counts[key] = sum(
                1 for a in assignments
                if a.model == model and a.role == role
            )

    # Check balance
    all_counts = list(counts.values())
    min_count = min(all_counts) if all_counts else 0
    max_count = max(all_counts) if all_counts else 0

    return {
        "counts": {f"{m}_{r}": c for (m, r), c in counts.items()},
        "min_count": min_count,
        "max_count": max_count,
        "is_balanced": max_count - min_count <= 1,
        "total_assignments": len(assignments),
    }


def create_study_design(
    models: list[str],
    competition_levels: list[str],
    constraint_levels: list[str],
    runs_per_condition: int,
) -> list[dict]:
    """
    Create a complete study design for Study 1 (baseline economic behavior).

    Args:
        models: List of model names
        competition_levels: Competition conditions (e.g., ["monopoly", "competitive"])
        constraint_levels: Constraint conditions (e.g., ["none", "prompt", "cba"])
        runs_per_condition: Number of runs per cell

    Returns:
        List of run configurations
    """
    design = []
    run_id = 0

    # Generate Latin square for model counterbalancing
    latin = LatinSquare(models)
    model_rotation = latin.generate_balanced()

    for competition in competition_levels:
        for constraint in constraint_levels:
            # Get counterbalanced model assignments for this cell
            for run_in_cell in range(runs_per_condition):
                # Rotate through models
                model_idx = run_in_cell % len(models)
                buyer_model = model_rotation[model_idx % len(model_rotation)][0]
                seller_model = model_rotation[model_idx % len(model_rotation)][
                    min(1, len(models) - 1)
                ] if len(models) > 1 else buyer_model

                config = {
                    "run_id": run_id,
                    "competition": competition,
                    "constraint": constraint,
                    "buyer_model": buyer_model,
                    "seller_model": seller_model,
                    "cell": f"{competition}_{constraint}",
                }
                design.append(config)
                run_id += 1

    return design
