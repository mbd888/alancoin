#!/usr/bin/env python3
"""
Unified CLI for the research harness.

Usage:
    python run.py study1 --mock --runs 2 --condition monopoly_cba
    python run.py study1 --live --api-url http://localhost:8080 --api-key sk_...
    python run.py study2 --mock --adversary overcharger
    python run.py cba-verify --live --api-url http://localhost:8080

After every run, writes a manifest.json for reproducibility.
"""

import argparse
import asyncio
import os
import random
import sys
from pathlib import Path

import numpy as np

# Add harness to path
sys.path.insert(0, str(Path(__file__).parent))

from harness.config import load_config, ExperimentConfig
from harness.runners import run_pre_study, run_study1, run_study2, run_study3
from harness.manifest import write_manifest


def seed_everything(seed: int):
    """Seed all RNGs for reproducibility."""
    random.seed(seed)
    np.random.seed(seed)


def main():
    parser = argparse.ArgumentParser(
        description="Research harness for LLM agent economic behavior experiments",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Quick mock run (no server, no API costs)
  python run.py study1 --mock --runs 2 --condition monopoly_cba

  # Live run through real gateway (requires 'make run')
  python run.py study1 --live --api-url http://localhost:8080 --api-key sk_test

  # Adversarial study with mock LLM
  python run.py study2 --mock --adversary overcharger --runs 3

  # CBA verification: live gateway proves enforcement is real
  python run.py cba-verify --live --api-url http://localhost:8080 --api-key sk_test

  # All studies (mock)
  python run.py all --mock
        """,
    )

    parser.add_argument(
        "study",
        choices=["pre_study", "study1", "study2", "study3", "cba-verify", "all"],
        help="Which study to run",
    )

    # Mode selection
    mode_group = parser.add_mutually_exclusive_group()
    mode_group.add_argument(
        "--mock", action="store_true",
        help="Use mock LLM + in-process market (no server needed)",
    )
    mode_group.add_argument(
        "--live", action="store_true",
        help="Route transactions through real gateway API",
    )

    # Gateway connection
    parser.add_argument("--api-url", type=str, default="http://localhost:8080")
    parser.add_argument(
        "--api-key", type=str,
        default=os.environ.get("ALANCOIN_API_KEY", ""),
        help="Gateway API key (default: $ALANCOIN_API_KEY env var)",
    )

    # Config
    parser.add_argument("--config", type=str, help="YAML config file")
    parser.add_argument("--output-dir", type=str, help="Output directory")
    parser.add_argument("--runs", type=int, help="Max runs (for testing)")
    parser.add_argument("--seed", type=int, default=42, help="Random seed")

    # Study-specific
    parser.add_argument("--condition", type=str, help="Study 1 condition filter")
    parser.add_argument("--adversary", type=str, help="Study 2 adversary filter")
    parser.add_argument("--decisions", type=int, help="Pre-study decisions")

    args = parser.parse_args()

    # Default to mock if neither specified
    if not args.mock and not args.live:
        args.mock = True
        print("No mode specified, defaulting to --mock\n")

    # Seed everything
    seed_everything(args.seed)

    # Load config
    if args.config:
        config = load_config(yaml_path=args.config, random_seed=args.seed)
    else:
        config = ExperimentConfig(random_seed=args.seed, mock_mode=args.mock)

    if args.mock:
        config.mock_mode = True

    output_dir = Path(args.output_dir) if args.output_dir else None

    # Run study
    if args.study == "pre_study":
        asyncio.run(run_pre_study(
            config=config, output_dir=output_dir, mock_mode=config.mock_mode,
        ))

    elif args.study == "study1":
        results = asyncio.run(run_study1(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            condition_filter=args.condition,
            max_runs=args.runs,
            live_mode=args.live,
            api_url=args.api_url,
            api_key=args.api_key,
        ))
        _write_manifest(args, config, output_dir, "study1")

    elif args.study == "study2":
        asyncio.run(run_study2(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            adversary_filter=args.adversary,
            max_runs=args.runs,
        ))
        _write_manifest(args, config, output_dir, "study2")

    elif args.study == "study3":
        asyncio.run(run_study3(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            max_runs=args.runs,
        ))
        _write_manifest(args, config, output_dir, "study3")

    elif args.study == "cba-verify":
        _run_cba_verify(args, config)

    elif args.study == "all":
        for study in ["pre_study", "study1", "study2", "study3"]:
            print(f"\n{'=' * 70}")
            print(f"  {study.upper()}")
            print(f"{'=' * 70}")
            if study == "pre_study":
                asyncio.run(run_pre_study(config=config, mock_mode=config.mock_mode))
            elif study == "study1":
                asyncio.run(run_study1(
                    config=config, mock_mode=config.mock_mode,
                    max_runs=args.runs, live_mode=args.live,
                    api_url=args.api_url, api_key=args.api_key,
                ))
            elif study == "study2":
                asyncio.run(run_study2(
                    config=config, mock_mode=config.mock_mode, max_runs=args.runs,
                ))
            elif study == "study3":
                asyncio.run(run_study3(
                    config=config, mock_mode=config.mock_mode, max_runs=args.runs,
                ))
        _write_manifest(args, config, output_dir, "all")
        print(f"\n{'=' * 70}")
        print("ALL STUDIES COMPLETE")
        print(f"{'=' * 70}")


def _run_cba_verify(args, config: ExperimentConfig):
    """Run CBA verification: 1 run of study1 with cba condition via live gateway."""
    if not args.live:
        print("ERROR: cba-verify requires --live mode (needs real gateway)")
        sys.exit(1)

    print("=" * 60)
    print("CBA VERIFICATION: Real Gateway Enforcement")
    print("=" * 60)
    print("Running study1 monopoly_cba via live gateway to prove CBA is real...")

    asyncio.run(run_study1(
        config=config,
        mock_mode=False,
        condition_filter="monopoly_cba",
        max_runs=1,
        live_mode=True,
        api_url=args.api_url,
        api_key=args.api_key,
    ))
    print("\nCBA verification complete. Check logs for gateway session creation/teardown.")


def _write_manifest(args, config: ExperimentConfig, output_dir, study_name: str):
    """Write reproducibility manifest after a run."""
    out = output_dir or Path(f"experiments/results/economic_behavior/{study_name}")
    path = write_manifest(
        output_dir=out,
        study_name=study_name,
        config_dict=config.model_dump(),
        seed=args.seed,
        extra={
            "mode": "live" if args.live else "mock",
            "cli_args": {
                "study": args.study,
                "mock": args.mock,
                "live": args.live,
                "condition": args.condition,
                "adversary": args.adversary,
                "runs": args.runs,
            },
        },
    )
    print(f"\nManifest written: {path}")


if __name__ == "__main__":
    main()
