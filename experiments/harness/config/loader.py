"""
YAML configuration loading with CLI override support.

Loads configuration from YAML files and allows overriding specific values
via command-line arguments.
"""

import os
from pathlib import Path
from typing import Any, Optional

import yaml

from .schema import ExperimentConfig


def load_yaml(path: str | Path) -> dict:
    """Load a YAML file and return as dictionary."""
    with open(path, "r") as f:
        return yaml.safe_load(f) or {}


def deep_merge(base: dict, override: dict) -> dict:
    """
    Deep merge two dictionaries.

    Values in override take precedence. Nested dicts are merged recursively.
    """
    result = base.copy()
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def merge_cli_overrides(config_dict: dict, overrides: dict[str, Any]) -> dict:
    """
    Merge CLI overrides into config dictionary.

    Supports dot-notation for nested keys:
        --market.num_buyers=4  -> config["market"]["num_buyers"] = 4

    Args:
        config_dict: Base configuration dictionary
        overrides: CLI overrides as key-value pairs

    Returns:
        Merged configuration dictionary
    """
    result = config_dict.copy()

    for key, value in overrides.items():
        if value is None:
            continue

        # Split dot-notation keys
        parts = key.split(".")
        target = result

        # Navigate to nested location
        for part in parts[:-1]:
            if part not in target:
                target[part] = {}
            target = target[part]

        # Set the value
        final_key = parts[-1]
        target[final_key] = value

    return result


def load_config(
    yaml_path: Optional[str | Path] = None,
    base_config_path: Optional[str | Path] = None,
    **cli_overrides: Any,
) -> ExperimentConfig:
    """
    Load experiment configuration from YAML with CLI overrides.

    Args:
        yaml_path: Path to study-specific YAML config
        base_config_path: Path to base YAML config (merged first)
        **cli_overrides: CLI argument overrides (dot-notation supported)

    Returns:
        Validated ExperimentConfig instance

    Example:
        config = load_config(
            yaml_path="configs/study1.yaml",
            base_config_path="configs/base.yaml",
            mock_mode=True,
            market__num_buyers=4,  # Use __ for nested keys in kwargs
        )
    """
    config_dict = {}

    # Load base config if provided
    if base_config_path:
        base_path = Path(base_config_path)
        if base_path.exists():
            config_dict = load_yaml(base_path)

    # Load study-specific config
    if yaml_path:
        study_path = Path(yaml_path)
        if study_path.exists():
            study_config = load_yaml(study_path)
            config_dict = deep_merge(config_dict, study_config)

    # Apply environment variable overrides
    config_dict = apply_env_overrides(config_dict)

    # Convert __ to . for kwargs passed with double underscore
    normalized_overrides = {}
    for key, value in cli_overrides.items():
        normalized_key = key.replace("__", ".")
        normalized_overrides[normalized_key] = value

    # Apply CLI overrides
    config_dict = merge_cli_overrides(config_dict, normalized_overrides)

    # Validate and return
    return ExperimentConfig(**config_dict)


def apply_env_overrides(config_dict: dict) -> dict:
    """
    Apply environment variable overrides to configuration.

    Environment variables are prefixed with HARNESS_ and use __ for nesting:
        HARNESS_MOCK_MODE=true -> config["mock_mode"] = True
        HARNESS_MARKET__NUM_BUYERS=4 -> config["market"]["num_buyers"] = 4
    """
    result = config_dict.copy()

    # API keys from environment
    env_mappings = {
        "ALANCOIN_API_URL": "api_base_url",
        "HARNESS_MOCK_MODE": "mock_mode",
        "HARNESS_RANDOM_SEED": "random_seed",
    }

    for env_var, config_key in env_mappings.items():
        value = os.getenv(env_var)
        if value is not None:
            # Convert types
            if config_key == "mock_mode":
                value = value.lower() in ("true", "1", "yes")
            elif config_key == "random_seed":
                value = int(value)
            result[config_key] = value

    # Handle HARNESS_ prefixed variables
    for key, value in os.environ.items():
        if key.startswith("HARNESS_") and key not in env_mappings:
            # Remove prefix and convert to config key
            config_key = key[8:].lower()  # Remove "HARNESS_"
            parts = config_key.split("__")

            # Navigate to nested location
            target = result
            for part in parts[:-1]:
                if part not in target:
                    target[part] = {}
                target = target[part]

            # Try to parse value
            final_key = parts[-1]
            target[final_key] = parse_env_value(value)

    return result


def parse_env_value(value: str) -> Any:
    """Parse environment variable value to appropriate type."""
    # Boolean
    if value.lower() in ("true", "false"):
        return value.lower() == "true"

    # Integer
    try:
        return int(value)
    except ValueError:
        pass

    # Float
    try:
        return float(value)
    except ValueError:
        pass

    # List (comma-separated)
    if "," in value:
        return [parse_env_value(v.strip()) for v in value.split(",")]

    # String
    return value


def get_config_dir() -> Path:
    """Get the configs directory path."""
    return Path(__file__).parent.parent.parent / "configs"


def list_available_configs() -> list[str]:
    """List available configuration files."""
    config_dir = get_config_dir()
    if not config_dir.exists():
        return []
    return [f.stem for f in config_dir.glob("*.yaml")]
