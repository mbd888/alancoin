#!/usr/bin/env python3
"""
Main CLI entry point for running economic behavior experiments.

Usage:
    python run_studies.py pre_study --mock --decisions 100
    python run_studies.py study1 --mock --runs 10 --condition monopoly_none
    python run_studies.py study2 --mock --adversary overcharger
    python run_studies.py study3 --mock --runs 5
"""

import argparse
import asyncio
import sys
from pathlib import Path

# Add harness to path
sys.path.insert(0, str(Path(__file__).parent))

from harness.config import load_config, ExperimentConfig
from harness.runners import run_pre_study, run_study1, run_study2, run_study3


def main():
    import warnings
    warnings.warn(
        "run_studies.py is deprecated. Use 'python run.py' instead.",
        DeprecationWarning,
        stacklevel=2,
    )
    print("WARNING: run_studies.py is deprecated. Use 'python run.py' instead.\n")

    parser = argparse.ArgumentParser(
        description="Run LLM agent economic behavior experiments",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    # Run pre-study with mock LLM (no API costs)
    python run_studies.py pre_study --mock

    # Run Study 1 with a single condition
    python run_studies.py study1 --mock --condition monopoly_none --runs 5

    # Run Study 2 against specific adversary
    python run_studies.py study2 --mock --adversary overcharger

    # Run all studies with real LLMs (requires API keys)
    python run_studies.py all
        """
    )

    parser.add_argument(
        "study",
        choices=["pre_study", "study1", "study2", "study3", "all"],
        help="Which study to run",
    )

    parser.add_argument(
        "--mock",
        action="store_true",
        help="Use mock LLM provider (no API costs)",
    )

    parser.add_argument(
        "--live",
        action="store_true",
        help="Route transactions through real gateway API (requires running server)",
    )

    parser.add_argument(
        "--api-url",
        type=str,
        default="",
        help="Gateway API URL (default: http://localhost:8080)",
    )

    parser.add_argument(
        "--api-key",
        type=str,
        default="",
        help="Gateway API key for authentication",
    )

    parser.add_argument(
        "--config",
        type=str,
        help="Path to YAML configuration file",
    )

    parser.add_argument(
        "--output-dir",
        type=str,
        help="Output directory for results",
    )

    parser.add_argument(
        "--runs",
        type=int,
        help="Maximum number of runs (for testing)",
    )

    # Pre-study specific
    parser.add_argument(
        "--decisions",
        type=int,
        help="Number of decisions for pre-study",
    )

    # Study 1 specific
    parser.add_argument(
        "--condition",
        type=str,
        help="Specific condition to run (e.g., 'monopoly_none')",
    )

    # Study 2 specific
    parser.add_argument(
        "--adversary",
        type=str,
        help="Specific adversary type to test",
    )

    # Configuration overrides
    parser.add_argument(
        "--seed",
        type=int,
        default=42,
        help="Random seed for reproducibility",
    )

    args = parser.parse_args()

    # Load configuration
    config_path = args.config
    if config_path:
        config = load_config(yaml_path=config_path, random_seed=args.seed)
    else:
        config = ExperimentConfig(random_seed=args.seed, mock_mode=args.mock)

    # Override mock mode if specified
    if args.mock:
        config.mock_mode = True

    # Determine output directory
    output_dir = Path(args.output_dir) if args.output_dir else None

    # Run appropriate study
    if args.study == "pre_study":
        asyncio.run(run_pre_study(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
        ))

    elif args.study == "study1":
        asyncio.run(run_study1(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            condition_filter=args.condition,
            max_runs=args.runs,
            live_mode=args.live,
            api_url=args.api_url,
            api_key=args.api_key,
        ))

    elif args.study == "study2":
        asyncio.run(run_study2(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            adversary_filter=args.adversary,
            max_runs=args.runs,
        ))

    elif args.study == "study3":
        asyncio.run(run_study3(
            config=config,
            output_dir=output_dir,
            mock_mode=config.mock_mode,
            max_runs=args.runs,
        ))

    elif args.study == "all":
        print("Running all studies...")
        print("\n" + "=" * 70)
        print("PRE-STUDY")
        print("=" * 70)
        asyncio.run(run_pre_study(
            config=config,
            mock_mode=config.mock_mode,
        ))

        print("\n" + "=" * 70)
        print("STUDY 1")
        print("=" * 70)
        asyncio.run(run_study1(
            config=config,
            mock_mode=config.mock_mode,
            max_runs=args.runs,
        ))

        print("\n" + "=" * 70)
        print("STUDY 2")
        print("=" * 70)
        asyncio.run(run_study2(
            config=config,
            mock_mode=config.mock_mode,
            max_runs=args.runs,
        ))

        print("\n" + "=" * 70)
        print("STUDY 3")
        print("=" * 70)
        asyncio.run(run_study3(
            config=config,
            mock_mode=config.mock_mode,
            max_runs=args.runs,
        ))

        print("\n" + "=" * 70)
        print("ALL STUDIES COMPLETE")
        print("=" * 70)


if __name__ == "__main__":
    main()
