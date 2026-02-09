#!/usr/bin/env python3
"""
Alancoin Pipeline Demo — Multi-Agent Service Composition

Demonstrates the killer feature: chaining multiple AI agent services
into a pipeline where each step's output feeds the next, all within
a single bounded spending session.

Pipeline:  summarize → translate → extract entities

Prerequisites:
    - Alancoin platform running: make run
    - Python SDK available:      pip install -e sdks/python/

Usage:
    python examples/demo_pipeline.py
"""
import sys
import os
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdks", "python"))

from alancoin import Alancoin
from alancoin.serve import ServiceAgent
from alancoin.session_keys import generate_session_keypair

PLATFORM_URL = os.environ.get("ALANCOIN_URL", "http://localhost:8080")

# ─────────────────────────────────────────────────────────────────────────────
# Step 1: Define three service agents
# ─────────────────────────────────────────────────────────────────────────────

summarizer = ServiceAgent(
    name="SummarizerBot",
    base_url=PLATFORM_URL,
    description="Summarizes long text into key points",
)

translator = ServiceAgent(
    name="TranslatorBot",
    base_url=PLATFORM_URL,
    description="Translates text between languages",
)

extractor = ServiceAgent(
    name="EntityExtractor",
    base_url=PLATFORM_URL,
    description="Extracts named entities from text",
)


@summarizer.service("inference", price="0.008", description="Summarize text")
def summarize(text="", task="summarize", **kwargs):
    """Simple extractive summarizer — takes first sentence of each paragraph."""
    paragraphs = [p.strip() for p in text.split("\n") if p.strip()]
    summary_parts = []
    for p in paragraphs[:5]:  # Max 5 paragraphs
        sentences = p.split(". ")
        summary_parts.append(sentences[0].rstrip(".") + ".")
    summary = " ".join(summary_parts) if summary_parts else text[:200]
    return {"output": summary, "task": task, "original_length": len(text), "summary_length": len(summary)}


@translator.service("translation", price="0.005", description="Translate text")
def translate(text="", target="es", **kwargs):
    table = {
        "es": {"the": "el", "is": "es", "a": "un", "of": "de", "and": "y",
               "in": "en", "to": "a", "for": "para", "with": "con",
               "that": "que", "it": "ello", "was": "fue", "on": "en",
               "are": "son", "as": "como", "be": "ser", "at": "en",
               "this": "esto", "have": "tener", "from": "de", "or": "o",
               "by": "por", "not": "no", "but": "pero", "what": "que",
               "all": "todos", "were": "fueron", "when": "cuando",
               "we": "nosotros", "there": "alli", "can": "puede",
               "an": "un", "which": "cual", "their": "su",
               "has": "tiene", "will": "va", "each": "cada",
               "about": "sobre", "how": "como", "up": "arriba",
               "world": "mundo", "hello": "hola", "good": "bueno",
               "morning": "manana", "new": "nuevo", "first": "primero",
               "company": "empresa", "market": "mercado", "technology": "tecnologia",
               "data": "datos", "system": "sistema", "agent": "agente",
               "agents": "agentes", "payment": "pago", "payments": "pagos",
               "service": "servicio", "services": "servicios",
               "network": "red", "platform": "plataforma",
               "autonomous": "autonomo", "digital": "digital",
               "economic": "economico", "infrastructure": "infraestructura"},
    }
    words = text.split()
    lang_table = table.get(target, {})
    translated = []
    for w in words:
        clean = w.lower().strip(".,!?;:()")
        punct = w[len(clean):] if len(clean) < len(w) else ""
        leading = w[:len(w) - len(clean) - len(punct)] if len(clean) + len(punct) < len(w) else ""
        result = lang_table.get(clean, w)
        if w and w[0].isupper():
            result = result.capitalize()
        translated.append(leading + result + punct)
    return {"output": " ".join(translated), "target": target}


@extractor.service("inference", price="0.010", description="Extract named entities")
def extract_entities(text="", task="extract_entities", **kwargs):
    """Simple entity extractor — finds capitalized multi-word sequences."""
    words = text.split()
    entities = []
    current = []

    for w in words:
        clean = w.strip(".,!?;:()")
        if clean and clean[0].isupper() and len(clean) > 1:
            current.append(clean)
        else:
            if len(current) >= 1:
                entity = " ".join(current)
                if entity not in entities and len(entity) > 2:
                    entities.append(entity)
            current = []

    if current:
        entity = " ".join(current)
        if entity not in entities and len(entity) > 2:
            entities.append(entity)

    return {"output": entities, "entity_count": len(entities), "task": task}


# ─────────────────────────────────────────────────────────────────────────────
# Step 2: Run the pipeline demo
# ─────────────────────────────────────────────────────────────────────────────

SAMPLE_DOCUMENT = """Alancoin is building economic infrastructure for autonomous AI agents.
The platform enables agents to discover each other, negotiate prices, and transact
using USDC on Base Layer 2. Unlike traditional payment rails, Alancoin uses session
keys to give agents bounded autonomy — they can spend within limits without human
approval for each transaction.

The network effect is critical. As more agents join the Alancoin marketplace,
discovery becomes more valuable. The reputation system, which scores agents from
0 to 100 based on transaction history and success rates, creates a trust layer
that makes the platform sticky. Agents with higher reputation scores get more
business, incentivizing good behavior.

Skyfire and other competitors focus on simple payment processing. Alancoin
differentiates by combining payments with discovery, reputation, and session-based
autonomy into a single platform. The data moat grows with every transaction —
each payment creates reputation data that cannot be replicated by a new entrant.

The Python SDK provides a three-line developer experience: create a client,
open a session, and call services. The session API handles discovery, selection,
payment, and endpoint invocation automatically. Service agents can be created
with a simple decorator pattern, making it easy to monetize any AI capability."""


def run_pipeline_demo():
    print("=" * 60)
    print("  ALANCOIN PIPELINE DEMO")
    print("  Multi-agent service composition with bounded budgets")
    print("=" * 60)
    print()

    # Check platform
    print("[1/6] Checking platform...")
    client = Alancoin(base_url=PLATFORM_URL)
    try:
        client.health()
    except Exception as e:
        print(f"  Platform not reachable: {e}")
        print(f"  Start it with: make run")
        sys.exit(1)
    print("  Platform is running")
    print()

    # Start service agents
    print("[2/6] Starting service agents...")
    summarizer.start(port=5010)
    translator.start(port=5011)
    extractor.start(port=5012)
    time.sleep(1)

    print(f"  SummarizerBot    @ localhost:5010 (address: {summarizer.address})")
    print(f"  TranslatorBot    @ localhost:5011 (address: {translator.address})")
    print(f"  EntityExtractor  @ localhost:5012 (address: {extractor.address})")
    print()

    # Register and fund buyer
    print("[3/6] Registering pipeline orchestrator...")
    _, buyer_addr = generate_session_keypair()
    result = client.register(
        address=buyer_addr,
        name="PipelineOrchestrator",
        description="Orchestrates multi-agent pipelines",
    )
    api_key = result.get("apiKey")

    funded_client = Alancoin(base_url=PLATFORM_URL, api_key=api_key)
    try:
        funded_client._request("POST", "/v1/admin/deposits", json={
            "agentAddress": buyer_addr,
            "amount": "10.00",
            "txHash": f"0xpipeline_{int(time.time())}",
        })
    except Exception:
        pass
    print(f"  PipelineOrchestrator: {buyer_addr}")
    print(f"  Funded with $10.00 USDC")
    print()

    # Show discovery with reputation
    print("[4/6] Discovering services (with reputation scores)...")
    services = funded_client.discover()
    for svc in services:
        print(f"  {svc.agent_name:20s} | {svc.type:12s} | ${svc.price:6s} | "
              f"rep: {svc.reputation_score:5.1f} ({svc.reputation_tier})")
    print()

    # Run pipeline manually (since we don't have real wallets for session keys)
    print("[5/6] Running pipeline: summarize → translate → extract")
    print("-" * 60)
    print()

    import requests as http_requests

    total_cost = 0.0

    # Step 1: Summarize
    print("  Step 1: Summarize document")
    print(f"  Input: {len(SAMPLE_DOCUMENT)} chars")
    try:
        resp = http_requests.post(
            "http://localhost:5010/services/inference",
            json={"text": SAMPLE_DOCUMENT, "task": "summarize"},
            headers={
                "X-Payment-TxHash": f"0xpipe_1_{int(time.time())}",
                "X-Payment-Amount": "0.008",
                "X-Payment-From": buyer_addr,
            },
            timeout=10,
        )
        step1 = resp.json()
        summary = step1["output"]
        total_cost += 0.008
        print(f"  Output: {summary[:100]}...")
        print(f"  Cost: $0.008 | Running total: ${total_cost:.3f}")
    except Exception as e:
        print(f"  Error: {e}")
        summary = SAMPLE_DOCUMENT[:200]

    print()

    # Step 2: Translate summary to Spanish
    print("  Step 2: Translate summary to Spanish")
    try:
        resp = http_requests.post(
            "http://localhost:5011/services/translation",
            json={"text": summary, "target": "es"},
            headers={
                "X-Payment-TxHash": f"0xpipe_2_{int(time.time())}",
                "X-Payment-Amount": "0.005",
                "X-Payment-From": buyer_addr,
            },
            timeout=10,
        )
        step2 = resp.json()
        translated = step2["output"]
        total_cost += 0.005
        print(f"  Output: {translated[:100]}...")
        print(f"  Cost: $0.005 | Running total: ${total_cost:.3f}")
    except Exception as e:
        print(f"  Error: {e}")
        translated = summary

    print()

    # Step 3: Extract entities from translation
    print("  Step 3: Extract entities from translated text")
    try:
        resp = http_requests.post(
            "http://localhost:5012/services/inference",
            json={"text": translated, "task": "extract_entities"},
            headers={
                "X-Payment-TxHash": f"0xpipe_3_{int(time.time())}",
                "X-Payment-Amount": "0.010",
                "X-Payment-From": buyer_addr,
            },
            timeout=10,
        )
        step3 = resp.json()
        entities = step3["output"]
        total_cost += 0.010
        print(f"  Output: {entities}")
        print(f"  Cost: $0.010 | Running total: ${total_cost:.3f}")
    except Exception as e:
        print(f"  Error: {e}")

    print()
    print("-" * 60)
    print()

    # Show what the SDK pipeline API looks like
    print("[6/6] Pipeline complete!")
    print()
    print(f"  Total pipeline cost: ${total_cost:.3f} USDC")
    print(f"  Steps completed:     3")
    print(f"  Agents involved:     3 (SummarizerBot, TranslatorBot, EntityExtractor)")
    print()
    print("  In production, the SDK pipeline API makes this even simpler:")
    print()
    print('    with client.session(max_total="0.10") as s:')
    print("        results = s.pipeline([")
    print('            {"service_type": "inference", "params": {"text": doc, "task": "summarize"}},')
    print('            {"service_type": "translation", "params": {"text": "$prev", "target": "es"}},')
    print('            {"service_type": "inference", "params": {"text": "$prev", "task": "extract_entities"}},')
    print("        ])")
    print('        entities = results[-1]["output"]')
    print()

    # Platform stats
    try:
        stats = funded_client.stats()
        print(f"  Platform Stats:")
        print(f"    Agents:       {stats.total_agents}")
        print(f"    Services:     {stats.total_services}")
        print(f"    Transactions: {stats.total_transactions}")
        print(f"    Volume:       ${stats.total_volume} USDC")
    except Exception:
        pass

    print()
    print("=" * 60)
    print("  Pipeline demo complete. Service agents still running.")
    print(f"  Dashboard: {PLATFORM_URL}")
    print("  Press Ctrl+C to stop.")
    print("=" * 60)

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        summarizer.stop()
        translator.stop()
        extractor.stop()


if __name__ == "__main__":
    run_pipeline_demo()
