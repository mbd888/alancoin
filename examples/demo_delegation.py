#!/usr/bin/env python3
"""
Alancoin A2A Delegation Demo

Demonstrates hierarchical agent-to-agent delegation:
1. Start 3 service agents: ResearchBot (delegator), TranslatorBot, SummarizerBot
2. Human creates a $10 session key
3. Calls ResearchBot which autonomously hires TranslatorBot and SummarizerBot
4. Budget accounting shows cascading spend through the delegation tree

Prerequisites:
    - Alancoin platform running: make run   (localhost:8080)
    - Python SDK available:      pip install -e sdks/python/

Usage:
    python examples/demo_delegation.py
"""
import sys
import os
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdks", "python"))

from alancoin import Alancoin, DelegationContext
from alancoin.serve import ServiceAgent
from alancoin.session_keys import generate_session_keypair, SessionKeyManager

PLATFORM_URL = os.environ.get("ALANCOIN_URL", "http://localhost:8080")


# -----------------------------------------------------------------------------
# Step 1: Define service agents
# -----------------------------------------------------------------------------

# TranslatorBot: simple word-level translator
translator = ServiceAgent(
    name="TranslatorBot",
    base_url=PLATFORM_URL,
    description="Translates text between languages",
)


@translator.service("translation", price="0.005", description="Translate text")
def translate(text="", target="es", **kwargs):
    table = {
        "es": {"hello": "hola", "world": "mundo", "the": "el", "document": "documento",
               "summary": "resumen", "is": "es", "this": "esto", "a": "un",
               "research": "investigacion", "about": "sobre", "topic": "tema"},
        "fr": {"hello": "bonjour", "world": "monde", "the": "le", "document": "document",
               "summary": "resume", "is": "est", "this": "ceci", "a": "un",
               "research": "recherche", "about": "sur", "topic": "sujet"},
    }
    words = text.split()
    lang_table = table.get(target, {})
    translated = []
    for w in words:
        clean = w.lower().strip(".,!?")
        punct = w[len(clean):] if len(clean) < len(w) else ""
        result = lang_table.get(clean, w)
        if w and w[0].isupper():
            result = result.capitalize()
        translated.append(result + punct)
    return {"output": " ".join(translated), "target": target, "agent": "TranslatorBot"}


# SummarizerBot: simple extractive summarizer
summarizer = ServiceAgent(
    name="SummarizerBot",
    base_url=PLATFORM_URL,
    description="Summarizes text to key points",
)


@summarizer.service("inference", price="0.008", description="Summarize text")
def summarize(text="", task="summarize", **kwargs):
    sentences = [s.strip() for s in text.split(".") if s.strip()]
    summary = ". ".join(sentences[:2]) + "." if sentences else text
    return {"output": summary, "task": task, "sentences": len(sentences), "agent": "SummarizerBot"}


# ResearchBot: orchestrator that delegates to other agents
research_bot = ServiceAgent(
    name="ResearchBot",
    base_url=PLATFORM_URL,
    description="Research agent that delegates to specialists",
    enable_delegation=True,
)


@research_bot.service("research", price="0.010", description="Research and process documents")
def research(text="", ctx: DelegationContext = None, **kwargs):
    """Research handler that delegates to TranslatorBot and SummarizerBot."""
    results = {"input": text, "agent": "ResearchBot", "steps": []}

    if ctx:
        # Step 1: Summarize the document
        try:
            summary_result = ctx.delegate(
                "inference",
                max_budget="0.010",
                text=text,
                task="summarize",
            )
            results["steps"].append({
                "step": "summarize",
                "output": summary_result.get("output", ""),
                "agent": summary_result.get("agent", "unknown"),
                "cost": "0.008",
            })
            summary_text = summary_result.get("output", text)
        except Exception as e:
            results["steps"].append({"step": "summarize", "error": str(e)})
            summary_text = text

        # Step 2: Translate the summary
        try:
            translate_result = ctx.delegate(
                "translation",
                max_budget="0.008",
                text=summary_text,
                target="es",
            )
            results["steps"].append({
                "step": "translate",
                "output": translate_result.get("output", ""),
                "agent": translate_result.get("agent", "unknown"),
                "cost": "0.005",
            })
        except Exception as e:
            results["steps"].append({"step": "translate", "error": str(e)})

        results["output"] = f"Research complete. {len(results['steps'])} sub-tasks delegated."
        results["delegation_used"] = True
    else:
        # No delegation context -- just echo back
        results["output"] = f"Research complete (no delegation). Input: {text[:100]}"
        results["delegation_used"] = False

    return results


# -----------------------------------------------------------------------------
# Step 2: Setup
# -----------------------------------------------------------------------------

def check_platform():
    """Verify the platform is running."""
    client = Alancoin(base_url=PLATFORM_URL)
    try:
        client.health()
        return True
    except Exception as e:
        print(f"  Platform not reachable at {PLATFORM_URL}: {e}")
        print(f"  Start it with: make run")
        return False


def register_buyer():
    """Register a buyer agent and fund it."""
    _, buyer_addr = generate_session_keypair()
    client = Alancoin(base_url=PLATFORM_URL)

    result = client.register(
        address=buyer_addr,
        name="DelegationBuyer",
        description="Demo buyer testing A2A delegation",
    )
    api_key = result.get("apiKey")

    # Fund via admin deposit
    funded_client = Alancoin(base_url=PLATFORM_URL, api_key=api_key)
    try:
        funded_client._request(
            "POST",
            "/v1/admin/deposits",
            json={
                "agentAddress": buyer_addr,
                "amount": "10.00",
                "txHash": f"0xdemo_delegation_{int(time.time())}",
            },
        )
        print(f"  Funded with $10.00 USDC platform balance")
    except Exception as e:
        print(f"  Note: Could not fund buyer: {e}")

    return buyer_addr, api_key


# -----------------------------------------------------------------------------
# Step 3: Run the demo
# -----------------------------------------------------------------------------

def print_tree(tree, indent=0):
    """Pretty-print a delegation tree."""
    prefix = "  " * indent
    label = tree.get("label") or "(root)"
    active = "active" if tree.get("active") else "revoked"
    remaining = tree.get("remaining", "?")
    spent = tree.get("totalSpent", "0")
    max_total = tree.get("maxTotal", "?")
    tx_count = tree.get("transactionCount", 0)

    print(f"{prefix}{'+-' if indent > 0 else ''} {tree['keyId'][:16]}...")
    print(f"{prefix}{'| ' if indent > 0 else '  '}Label: {label}")
    print(f"{prefix}{'| ' if indent > 0 else '  '}Budget: ${spent} / ${max_total} (remaining: ${remaining})")
    print(f"{prefix}{'| ' if indent > 0 else '  '}Txs: {tx_count}, Status: {active}")

    for child in tree.get("children", []):
        print_tree(child, indent + 1)


def run_demo():
    print("=" * 70)
    print("  ALANCOIN A2A DELEGATION DEMO")
    print("  Agents autonomously hiring other agents")
    print("=" * 70)
    print()

    # Check platform
    print("[1/6] Checking platform...")
    if not check_platform():
        sys.exit(1)
    print("  Platform is running")
    print()

    # Start service agents
    print("[2/6] Starting service agents...")
    translator.start(port=5010)
    summarizer.start(port=5011)
    research_bot.start(port=5012)
    time.sleep(1.5)

    print(f"  TranslatorBot  @ :5010 ({translator.address})")
    print(f"  SummarizerBot  @ :5011 ({summarizer.address})")
    print(f"  ResearchBot    @ :5012 ({research_bot.address})")
    print()

    # Register buyer
    print("[3/6] Registering buyer agent...")
    buyer_addr, api_key = register_buyer()
    print(f"  DelegationBuyer: {buyer_addr}")
    print()

    # Create session key for the buyer
    print("[4/6] Creating session key with $5.00 budget...")
    client = Alancoin(base_url=PLATFORM_URL, api_key=api_key)
    skm = SessionKeyManager()

    key = client.create_session_key(
        agent_address=buyer_addr,
        public_key=skm.public_key,
        expires_in="1h",
        max_per_transaction="2.00",
        max_total="5.00",
        allow_any=True,
        label="delegation-demo",
    )
    key_id = key.get("id") or key.get("session", {}).get("id")
    skm.set_key_id(key_id)
    print(f"  Session key: {key_id}")
    print(f"  Budget: $5.00 total, $2.00 per tx")
    print()

    # Call ResearchBot directly with delegation
    print("[5/6] Calling ResearchBot with delegation...")
    print("-" * 70)

    import requests

    # First, pay for the research service
    tx_result = skm.transact(
        client, buyer_addr,
        research_bot.address,
        "0.010",
    )
    print(f"  Paid ResearchBot: $0.010 (tx: {tx_result.get('status', 'ok')})")

    # Create a child session key for ResearchBot to use
    child_skm = SessionKeyManager()
    signed = skm.sign_delegation(child_skm.public_key, "2.00")
    child_resp = client.create_child_session_key(
        parent_key_id=key_id,
        delegation_label="research-delegation",
        allow_any=True,
        **signed,
    )
    child_key = child_resp.get("childKey", {})
    child_key_id = child_key.get("id")
    print(f"  Created child key for ResearchBot: {child_key_id}")
    print(f"  Child budget: $2.00 (depth: {child_key.get('depth', 1)})")
    print()

    # Call the research endpoint with delegation headers
    print("  >> Calling ResearchBot: research 'The document is about research topic'")
    try:
        resp = requests.post(
            "http://localhost:5012/services/research",
            json={
                "text": "The document is a summary about research topic",
            },
            headers={
                "X-Payment-TxHash": tx_result.get("txHash", f"0xdemo_{int(time.time())}"),
                "X-Payment-Amount": "0.010",
                "X-Payment-From": buyer_addr,
                "X-Delegation-KeyId": child_key_id,
                "X-Delegation-Budget": "2.00",
                "X-Delegation-PrivateKey": child_skm.private_key,
                "X-Delegation-Depth": str(child_key.get("depth", 1)),
            },
            timeout=30,
        )
        result = resp.json()

        print(f"  << Result: {result.get('output', 'no output')}")
        print(f"     Delegation used: {result.get('delegation_used', False)}")
        if result.get("steps"):
            print(f"     Sub-tasks:")
            for step in result["steps"]:
                if "error" in step:
                    print(f"       - {step['step']}: ERROR: {step['error']}")
                else:
                    output = step.get("output", "")
                    if len(output) > 60:
                        output = output[:60] + "..."
                    print(f"       - {step['step']} ({step.get('agent', '?')}): {output} [${step.get('cost', '?')}]")
    except Exception as e:
        print(f"  << Error: {e}")

    print()
    print("-" * 70)

    # Show delegation tree
    print()
    print("[6/6] Delegation tree:")
    print("-" * 70)
    try:
        tree_resp = client.get_delegation_tree(key_id)
        tree = tree_resp.get("tree", {})
        print()
        print_tree(tree)
    except Exception as e:
        print(f"  Could not fetch tree: {e}")

    print()
    print("-" * 70)
    print()

    # Show the API
    print("  Delegation in the SDK:")
    print()
    print("    # Parent creates child key for sub-agent")
    print("    signed = parent_skm.sign_delegation(child_skm.public_key, '2.00')")
    print("    child = client.create_child_session_key(parent_key_id, **signed)")
    print()
    print("    # Or use the high-level API:")
    print("    with client.session(max_total='10.00') as s:")
    print("        result = s.call_service_with_delegation(")
    print("            'research', delegation_budget='3.00', text='...')")
    print()

    print("=" * 70)
    print("  Demo complete. Agents still running.")
    print(f"  Dashboard: {PLATFORM_URL}")
    print("  Press Ctrl+C to stop.")
    print("=" * 70)

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        translator.stop()
        summarizer.stop()
        research_bot.stop()


if __name__ == "__main__":
    run_demo()
