#!/usr/bin/env python3
"""
Smoke test: runs study1 --mock --runs 1, verifies output files exist and are valid.

This is a quick sanity check that the harness plumbing works end-to-end.
Does NOT validate scientific results — only that files are produced and parseable.

Usage:
    python smoke_test.py
"""

import asyncio
import json
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from harness.config import ExperimentConfig
from harness.runners import run_study1


def main():
    print("=" * 60)
    print("SMOKE TEST: Mock end-to-end")
    print("=" * 60)

    errors = []
    tmpdir = Path(tempfile.mkdtemp(prefix="harness_smoke_"))

    try:
        # Run study1 with 2 mock runs
        config = ExperimentConfig(random_seed=42, mock_mode=True)
        results = asyncio.run(run_study1(
            config=config,
            output_dir=tmpdir,
            mock_mode=True,
            condition_filter="monopoly_cba",
            max_runs=2,
        ))

        # Check 1: results object is valid
        if not results.runs:
            errors.append("No runs returned")
        else:
            print(f"\n  [OK] {len(results.runs)} run(s) completed")

        # Check 2: output files exist
        jsonl_files = list(tmpdir.glob("*_runs.jsonl"))
        summary_files = list(tmpdir.glob("*_summary.json"))

        if not jsonl_files:
            errors.append("No runs.jsonl file found")
        else:
            print(f"  [OK] runs.jsonl exists: {jsonl_files[0].name}")

        if not summary_files:
            errors.append("No summary.json file found")
        else:
            print(f"  [OK] summary.json exists: {summary_files[0].name}")

        # Check 3: JSONL is valid
        if jsonl_files:
            with open(jsonl_files[0]) as f:
                for i, line in enumerate(f):
                    try:
                        data = json.loads(line)
                        assert "condition" in data
                        assert "avg_price_ratio" in data
                    except (json.JSONDecodeError, AssertionError) as e:
                        errors.append(f"Invalid JSONL line {i}: {e}")
            if not errors:
                print(f"  [OK] JSONL is valid JSON with expected fields")

        # Check 4: summary.json is valid
        if summary_files:
            with open(summary_files[0]) as f:
                summary = json.load(f)
                assert "total_runs" in summary
                assert "by_condition" in summary
            print(f"  [OK] summary.json is valid with expected structure")

        # Check 5: results have expected condition
        for run in results.runs:
            if run.condition != "monopoly_cba":
                errors.append(f"Expected condition 'monopoly_cba', got '{run.condition}'")
        if not errors:
            print(f"  [OK] All runs have correct condition")

        # Check 6: log files produced (may be in subdirs)
        log_files = list(tmpdir.rglob("*.jsonl"))
        if len(log_files) < 2:
            errors.append(f"Expected at least 2 JSONL files (runs + logs), got {len(log_files)}")
        else:
            print(f"  [OK] {len(log_files)} JSONL files produced (incl. subdirs)")

    except Exception as e:
        errors.append(f"Exception during smoke test: {e}")
        import traceback
        traceback.print_exc()
    finally:
        shutil.rmtree(tmpdir, ignore_errors=True)

    # Report
    print()
    if errors:
        print("SMOKE TEST FAILED:")
        for err in errors:
            print(f"  - {err}")
        sys.exit(1)
    else:
        print("SMOKE TEST PASSED")
        sys.exit(0)


if __name__ == "__main__":
    main()
