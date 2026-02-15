# Research Harness: LLM Agent Economic Behavior

Research harness for studying how LLM agents reason about money in simulated and real marketplaces.

## Architecture

```
run.py                         Unified CLI entry point
harness/
├── agents/                    LLM-powered buyer/seller agents
│   └── providers/             OpenAI, Anthropic, Together, Mock
├── adversaries/               5 adversary types (overcharger, injection, etc.)
├── analysis/                  Statistics, trace analysis, figures
├── clients/
│   ├── mock_market.py         In-process market simulation
│   ├── gateway_market.py      Real gateway API client
│   └── market_backend.py      Protocol both implement
├── config/                    Pydantic config schema + YAML loader
├── logging/                   Structured JSONL logger + cost tracker
├── market/                    Orchestrator, negotiation, delivery
│   └── orchestrator.py        Runs discovery→negotiation→transaction→delivery
├── runners/                   Study-specific experiment runners
│   ├── study1.py              Baseline economic behavior (2×3 factorial)
│   ├── study2.py              Adversarial resilience (5 attack types)
│   └── study3.py              Reputation dynamics (exploratory)
└── manifest.py                Reproducibility manifest writer
```

**Two market backends:**
- `--mock`: `MockMarket` — in-process Python simulation. CBA is a Python `if` statement. Fast, free, good for development.
- `--live`: `GatewayMarket` — routes transactions through the real Go gateway API. CBA enforcement happens in Go + PostgreSQL (hold/settle/release, policy evaluation). **This is the path that makes the paper's claim non-tautological.**

## Quick Start

### Mock mode (3 commands, no server)

```bash
cd experiments
pip install -e ".[all]"
python run.py study1 --mock --runs 2 --condition monopoly_cba
```

### Live mode (5 commands, requires server)

```bash
cd experiments
pip install -e ".[all]"
# In another terminal: make run
python run.py study1 --live --runs 2 --condition monopoly_cba \
  --api-url http://localhost:8080 --api-key sk_your_key
```

### Docker (full stack)

```bash
cd experiments
docker compose up
```

## Studies

| Study | Design | DV | IV | Runs |
|-------|--------|----|----|------|
| **Study 1** | 2×3 factorial | Price ratio, task completion, budget utilization | Competition (monopoly/competitive) × Constraint (none/prompt/cba) | 30/cell |
| **Study 2** | Dose-response | Exploitation rate, economic damage | 5 adversary types × 3 defense conditions × intensity levels | 15/cell |
| **Study 3** | Exploratory | Reputation effects on pricing | 3 reputation conditions × 30 rounds | 10/cond |

## CLI Reference

```bash
# Study 1: baseline economic behavior
python run.py study1 --mock --runs 5 --condition monopoly_cba

# Study 2: adversarial resilience
python run.py study2 --mock --adversary overcharger --runs 3

# CBA verification (proves enforcement is real, not simulated)
python run.py cba-verify --live --api-url http://localhost:8080 --api-key sk_...

# All studies
python run.py all --mock --runs 5

# With real LLMs (requires API keys in environment)
OPENAI_API_KEY=sk-... python run.py study1 --live --runs 30
```

## Cost Estimates

| Study | Calls/run | Runs (full) | Est. cost (GPT-4o) |
|-------|-----------|-------------|---------------------|
| Study 1 | ~15 | 180 | ~$27 |
| Study 2 | ~10 | 3,375 | ~$340 |
| Study 3 | ~60 | 30 | ~$18 |

Per-study hard limits are configurable in the YAML config (`logging.cost_limits`). Default: Study 1 = $50, Study 2 = $100.

## Testing

```bash
# Unit tests (109 tests, <1s)
python -m pytest tests/ -v

# Smoke test (mock end-to-end, ~15s)
python smoke_test.py

# Integration test (requires running server)
ALANCOIN_API_URL=http://localhost:8080 ALANCOIN_API_KEY=sk_... python integration_test.py

# From project root
make test-harness
make smoke-test
```

## Reproducibility

Every run writes a `manifest.json` with:
- Git hash + dirty flag
- Python version + pip freeze
- Full config snapshot
- Random seed
- Cost summary

## Output Structure

```
results/economic_behavior/study1/
├── study1_20260214_120000_runs.jsonl     # One JSON object per run
├── study1_20260214_120000_summary.json   # Aggregated statistics
├── manifest.json                          # Reproducibility manifest
└── study1/                               # Structured event logs
    ├── ..._llm_call.jsonl
    ├── ..._transaction.jsonl
    └── ..._negotiation.jsonl
```

## Configuration

Default config is built into `harness/config/schema.py`. Override with YAML:

```bash
python run.py study1 --config configs/my_config.yaml --mock
```

Environment variables (prefix `HARNESS_`):
```bash
HARNESS_MOCK_MODE=true
HARNESS_RANDOM_SEED=123
ALANCOIN_API_URL=http://localhost:8080
```

## Citation

```bibtex
@inproceedings{alancoin2026cba,
  title={Cryptographically Bounded Autonomy: Safe Economic Delegation for LLM Agents},
  author={...},
  booktitle={},
  year={2026}
}
```
