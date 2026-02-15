"""
Reproducibility manifest for experiment runs.

After every run, writes a manifest.json capturing the full environment
so any result can be reproduced: git hash, python version, pip freeze,
full config, seed, cost, timestamp.
"""

import json
import os
import platform
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


def _get_git_hash() -> str:
    """Get current git commit hash, or 'unknown' if not in a git repo."""
    try:
        result = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            capture_output=True, text=True, timeout=5,
        )
        return result.stdout.strip() if result.returncode == 0 else "unknown"
    except Exception:
        return "unknown"


def _get_git_dirty() -> bool:
    """Check if the working tree has uncommitted changes."""
    try:
        result = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True, text=True, timeout=5,
        )
        return bool(result.stdout.strip()) if result.returncode == 0 else False
    except Exception:
        return False


def _get_pip_freeze() -> list[str]:
    """Get installed packages via pip freeze."""
    try:
        result = subprocess.run(
            [sys.executable, "-m", "pip", "freeze", "--local"],
            capture_output=True, text=True, timeout=15,
        )
        if result.returncode == 0:
            return [line for line in result.stdout.strip().split("\n") if line]
    except Exception:
        pass
    return []


_SENSITIVE_KEYS = {"api_key", "api_keys", "secret", "password", "token", "credential"}


def _scrub_secrets(obj: Any) -> Any:
    """Recursively redact values whose keys look like secrets."""
    if isinstance(obj, dict):
        return {
            k: ("***REDACTED***" if any(s in k.lower() for s in _SENSITIVE_KEYS) else _scrub_secrets(v))
            for k, v in obj.items()
        }
    if isinstance(obj, list):
        return [_scrub_secrets(item) for item in obj]
    return obj


def write_manifest(
    output_dir: Path,
    study_name: str,
    config_dict: dict,
    seed: int,
    cost_summary: Optional[dict] = None,
    extra: Optional[dict] = None,
) -> Path:
    """
    Write a reproducibility manifest after a run.

    Args:
        output_dir: Directory to write manifest.json into.
        study_name: Name of the study (e.g., 'study1').
        config_dict: Full experiment configuration as dict.
        seed: Random seed used.
        cost_summary: Cost tracker summary dict.
        extra: Any additional metadata.

    Returns:
        Path to the written manifest file.
    """
    manifest = {
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "study": study_name,
        "seed": seed,
        "environment": {
            "python_version": platform.python_version(),
            "platform": platform.platform(),
            "machine": platform.machine(),
            "git_hash": _get_git_hash(),
            "git_dirty": _get_git_dirty(),
        },
        "packages": _get_pip_freeze(),
        "config": _scrub_secrets(config_dict),
    }

    if cost_summary:
        manifest["cost"] = cost_summary

    if extra:
        manifest.update(_scrub_secrets(extra))

    output_dir.mkdir(parents=True, exist_ok=True)
    manifest_path = output_dir / "manifest.json"
    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2, default=str)

    return manifest_path
