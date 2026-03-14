#!/usr/bin/env python3
"""
Alancoin Gateway Demo Script

Demonstrates the complete AI agent payment flow end-to-end:
1. Agent registration with real service endpoints
2. Gateway session management (budgeted payment sessions)
3. Gateway proxy calls (discover -> hold -> forward -> settle)
4. Multi-agent pipeline via gateway
5. Escrow protection for high-value transactions
6. Reputation building through real service interactions
7. Session keys for bounded autonomy

The stub service provider (scripts/stub_provider.py) runs as a subprocess,
providing real HTTP endpoints that the gateway proxy forwards requests to.

Usage:
    python3 scripts/demo.py [--url http://localhost:8080] [--speed fast|normal|slow] [--demo]
    python3 scripts/demo.py --speed fast --demo   # Quick automated run
"""

import argparse
import atexit
import os
import random
import secrets
import signal
import subprocess
import sys
import time
from datetime import datetime
from typing import Optional

import requests

# Demo configuration
BASE_URL = "http://localhost:8080"
STUB_PROVIDER_PORT = 9090
STUB_PROVIDER_BASE = f"http://localhost:{STUB_PROVIDER_PORT}"

# Agent personas for the demo — each registers services WITH endpoint URLs
# pointing to the stub provider, so the gateway proxy can forward real requests.
AGENTS = [
    {
        "name": "TranslatorBot",
        "description": "High-quality language translation powered by GPT-4",
        "services": [
            {"type": "translation", "name": "English to Spanish", "price": "0.001",
             "endpoint": f"{STUB_PROVIDER_BASE}/translation"},
            {"type": "translation", "name": "English to French", "price": "0.0015",
             "endpoint": f"{STUB_PROVIDER_BASE}/translation"},
            {"type": "translation", "name": "English to German", "price": "0.002",
             "endpoint": f"{STUB_PROVIDER_BASE}/translation"},
        ],
    },
    {
        "name": "ResearchAgent",
        "description": "Deep web research and comprehensive summarization",
        "services": [
            {"type": "inference", "name": "Web Research", "price": "0.05",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
            {"type": "inference", "name": "SEC Filing Summary", "price": "0.10",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
            {"type": "inference", "name": "Competitive Analysis", "price": "0.15",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
        ],
    },
    {
        "name": "CodeReviewBot",
        "description": "Automated code review with security analysis",
        "services": [
            {"type": "code-review", "name": "Code Review", "price": "0.02",
             "endpoint": f"{STUB_PROVIDER_BASE}/code-review"},
            {"type": "code-review", "name": "Test Generation", "price": "0.03",
             "endpoint": f"{STUB_PROVIDER_BASE}/code-review"},
            {"type": "code-review", "name": "Security Audit", "price": "0.05",
             "endpoint": f"{STUB_PROVIDER_BASE}/code-review"},
        ],
    },
    {
        "name": "DataScraper",
        "description": "Real-time data extraction and monitoring",
        "services": [
            {"type": "inference", "name": "Price Feed", "price": "0.002",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
            {"type": "inference", "name": "Social Sentiment", "price": "0.01",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
        ],
    },
    {
        "name": "TradingAgent",
        "description": "DeFi analytics and yield optimization",
        "services": [
            {"type": "inference", "name": "Yield Analysis", "price": "0.08",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
            {"type": "inference", "name": "Risk Assessment", "price": "0.12",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
        ],
    },
    {
        "name": "ContentWriter",
        "description": "AI-powered content generation and editing",
        "services": [
            {"type": "inference", "name": "Blog Post", "price": "0.04",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
            {"type": "inference", "name": "Tweet Thread", "price": "0.01",
             "endpoint": f"{STUB_PROVIDER_BASE}/inference"},
        ],
    },
    {
        "name": "EmbeddingService",
        "description": "Text embedding and vector generation",
        "services": [
            {"type": "embedding", "name": "Text Embedding", "price": "0.005",
             "endpoint": f"{STUB_PROVIDER_BASE}/embedding"},
            {"type": "embedding", "name": "Batch Embedding", "price": "0.02",
             "endpoint": f"{STUB_PROVIDER_BASE}/embedding"},
        ],
    },
]


def random_address() -> str:
    """Generate a random Ethereum-style address."""
    return "0x" + secrets.token_hex(20)


def random_tx_hash() -> str:
    """Generate a random transaction hash."""
    return "0x" + secrets.token_hex(32)


class StubProviderProcess:
    """Manages the stub provider subprocess lifecycle."""

    def __init__(self, port: int = STUB_PROVIDER_PORT):
        self.port = port
        self.process = None

    def start(self):
        """Start the stub provider as a subprocess."""
        script_dir = os.path.dirname(os.path.abspath(__file__))
        stub_script = os.path.join(script_dir, "stub_provider.py")

        if not os.path.exists(stub_script):
            raise FileNotFoundError(f"Stub provider not found at {stub_script}")

        self.process = subprocess.Popen(
            [sys.executable, stub_script, "--port", str(self.port)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        # Wait briefly for the server to start
        time.sleep(0.5)

        # Check it's alive
        if self.process.poll() is not None:
            stderr = self.process.stderr.read().decode() if self.process.stderr else ""
            raise RuntimeError(f"Stub provider failed to start: {stderr}")

        # Verify it responds
        for _ in range(10):
            try:
                resp = requests.post(
                    f"http://localhost:{self.port}/inference",
                    json={"text": "test", "task": "classify"},
                    timeout=2,
                )
                if resp.status_code == 200:
                    return
            except requests.ConnectionError:
                time.sleep(0.3)

        raise RuntimeError("Stub provider started but not responding")

    def stop(self):
        """Stop the stub provider subprocess."""
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
            self.process = None


class DemoRunner:
    def __init__(self, base_url: str, speed: str = "normal", demo_mode: bool = False):
        self.base_url = base_url.rstrip("/")
        self.speed = speed
        self.demo_mode = demo_mode
        self.agents = {}  # name -> {address, api_key, services}
        self.running = True
        self.stub_provider = StubProviderProcess()

        # Speed settings (seconds between actions)
        self.delays = {
            "fast": {"tx": 0.2, "session": 0.5, "phase": 0.5},
            "normal": {"tx": 1.5, "session": 3, "phase": 2},
            "slow": {"tx": 4, "session": 10, "phase": 5},
        }[speed]

    def log(self, emoji: str, message: str):
        """Print a timestamped log message."""
        timestamp = datetime.now().strftime("%H:%M:%S")
        print(f"[{timestamp}] {emoji} {message}")

    def api_call(self, method: str, endpoint: str, json_data: dict = None,
                 api_key: str = None, extra_headers: dict = None,
                 retries: int = 2) -> Optional[dict]:
        """Make an API call with error handling and retry on rate limit."""
        url = f"{self.base_url}{endpoint}"
        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"
        if extra_headers:
            headers.update(extra_headers)

        for attempt in range(retries + 1):
            try:
                if method == "GET":
                    resp = requests.get(url, headers=headers, timeout=15)
                elif method == "POST":
                    resp = requests.post(url, json=json_data, headers=headers, timeout=15)
                elif method == "DELETE":
                    resp = requests.delete(url, headers=headers, timeout=15)
                else:
                    raise ValueError(f"Unknown method: {method}")

                if resp.status_code == 429 and attempt < retries:
                    time.sleep(1.0)
                    continue

                if resp.status_code >= 400:
                    return None
                return resp.json() if resp.text else {}
            except Exception as e:
                if attempt == retries:
                    self.log("!", f"API error: {e}")
                return None
        return None

    def api_call_raw(self, method: str, endpoint: str, json_data: dict = None,
                     api_key: str = None, extra_headers: dict = None):
        """Make an API call returning the raw response (for checking status codes)."""
        url = f"{self.base_url}{endpoint}"
        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"
        if extra_headers:
            headers.update(extra_headers)

        try:
            if method == "GET":
                return requests.get(url, headers=headers, timeout=15)
            elif method == "POST":
                return requests.post(url, json=json_data, headers=headers, timeout=15)
            elif method == "DELETE":
                return requests.delete(url, headers=headers, timeout=15)
            else:
                return None
        except Exception:
            return None

    # ── Phase 1: Agent Registration ──────────────────────────────────────

    def setup_agents(self):
        """Register all demo agents with service endpoints pointing to stub provider."""
        self.log("  ", "Registering AI agents with service endpoints...")

        total_services = 0
        for agent_config in AGENTS:
            address = random_address()

            result = self.api_call("POST", "/v1/agents", {
                "address": address,
                "name": agent_config["name"],
                "description": agent_config["description"],
            })

            if not result:
                self.log("!", f"Failed to register {agent_config['name']}")
                continue

            api_key = result.get("apiKey", "")
            self.agents[agent_config["name"]] = {
                "address": address,
                "api_key": api_key,
                "services": [],
            }

            for service in agent_config["services"]:
                svc_result = self.api_call(
                    "POST",
                    f"/v1/agents/{address}/services",
                    {
                        "type": service["type"],
                        "name": service["name"],
                        "description": f"{service['name']} service",
                        "price": service["price"],
                        "endpoint": service["endpoint"],
                    },
                    api_key=api_key,
                )
                if svc_result:
                    self.agents[agent_config["name"]]["services"].append(service)
                    total_services += 1

            svc_count = len(self.agents[agent_config["name"]]["services"])
            self.log("  ", f"  {agent_config['name']:<20s} {svc_count} services (endpoints live)")

        self.log("[ok]", f"Registered {len(self.agents)} agents with {total_services} service endpoints")

    def fund_agents(self):
        """Fund all agents with initial USDC deposits."""
        self.log("  ", "Funding agents with initial USDC deposits...")

        funded = 0
        for name, agent in self.agents.items():
            result = self.api_call("POST", "/v1/admin/deposits", {
                "agentAddress": agent["address"],
                "amount": "10.00",
                "txHash": random_tx_hash(),
            }, api_key=agent["api_key"])

            if result:
                funded += 1

        self.log("[ok]", f"Funded {funded} agents with $10.00 USDC each")

    # ── Phase 2: Gateway Proxy (the core product flow) ───────────────────

    def demonstrate_gateway_proxy(self):
        """Demonstrate the full gateway proxy flow: session -> proxy -> settle."""
        self.log("  ", "GATEWAY PROXY FLOW")
        self.log("  ", "-" * 55)
        self.log("  ", "  The gateway handles: discover -> hold -> forward -> settle")
        self.log("  ", "")

        buyer_name = "TradingAgent"
        if buyer_name not in self.agents:
            self.log("!", "TradingAgent not found, skipping gateway demo")
            return

        buyer = self.agents[buyer_name]

        # Step 1: Create a gateway session with a budget
        self.log("  ", "  Step 1: Creating gateway session (budget: $1.00)")
        session_result = self.api_call(
            "POST",
            "/v1/gateway/sessions",
            {
                "maxTotal": "1.00",
                "maxPerRequest": "0.20",
                "strategy": "cheapest",
                "allowedTypes": ["inference", "translation", "code-review", "embedding"],
            },
            api_key=buyer["api_key"],
        )

        if not session_result:
            self.log("!", "Failed to create gateway session")
            self.log("  ", "  (Agent may need sufficient balance)")
            return

        session = session_result.get("session", {})
        token = session_result.get("token", session.get("id", ""))
        session_id = session.get("id", "")

        self.log("[ok]", f"Session created: {session_id[:16]}...")
        self.log("  ", f"  Budget: $1.00 | Per-request max: $0.20 | Strategy: cheapest")
        time.sleep(self.delays["phase"])

        # Step 2: Make gateway proxy calls (real HTTP forwarding!)
        self.log("  ", "")
        self.log("  ", "  Step 2: Making gateway proxy calls (real HTTP forwarding)")

        proxy_calls = [
            {
                "serviceType": "inference",
                "params": {"text": "Quarterly earnings show 15% revenue growth", "task": "summarize"},
                "label": "Summarize earnings report",
            },
            {
                "serviceType": "translation",
                "params": {"text": "AI agents can now transact autonomously", "target": "es"},
                "label": "Translate to Spanish",
            },
            {
                "serviceType": "code-review",
                "params": {"code": "def pay(amount):\n  return transfer(amount)\n"},
                "label": "Review payment code",
            },
            {
                "serviceType": "embedding",
                "params": {"text": "Machine learning payment infrastructure"},
                "label": "Generate embedding",
            },
        ]

        total_spent = 0.0
        successful_calls = 0

        for i, call in enumerate(proxy_calls, 1):
            result = self.api_call(
                "POST",
                "/v1/gateway/proxy",
                {
                    "serviceType": call["serviceType"],
                    "params": call["params"],
                },
                api_key=buyer["api_key"],
                extra_headers={"X-Gateway-Token": token},
            )

            if result:
                proxy_result = result.get("result", {})
                amount_paid = proxy_result.get("amountPaid", "0")
                service_name = proxy_result.get("serviceName", "unknown")
                remaining = result.get("remaining", "?")
                response = proxy_result.get("response", {})
                output_preview = str(response.get("output", ""))[:60]

                total_spent += float(amount_paid)
                successful_calls += 1

                self.log("  ", f"  [{i}/4] {call['label']:<28s} ${amount_paid} -> {service_name}")
                self.log("  ", f"         Response: {output_preview}...")
            else:
                self.log("  ", f"  [{i}/4] {call['label']:<28s} FAILED")

            time.sleep(self.delays["tx"])

        self.log("  ", "")
        self.log("[ok]", f"  {successful_calls} proxy calls completed | Spent: ${total_spent:.4f}")

        time.sleep(self.delays["phase"])

        # Step 3: Check session state
        self.log("  ", "")
        self.log("  ", "  Step 3: Checking session state")
        session_state = self.api_call(
            "GET",
            f"/v1/gateway/sessions/{session_id}",
            api_key=buyer["api_key"],
        )

        if session_state:
            s = session_state.get("session", {})
            self.log("  ", f"  Total spent: ${s.get('totalSpent', '?')} | "
                          f"Remaining: {s.get('id', '?')[:8]}... | "
                          f"Requests: {s.get('requestCount', 0)}")

        time.sleep(self.delays["phase"])

        # Step 4: Close the session (refund remaining budget)
        self.log("  ", "")
        self.log("  ", "  Step 4: Closing session (refund unspent budget)")
        close_result = self.api_call(
            "DELETE",
            f"/v1/gateway/sessions/{session_id}",
            api_key=buyer["api_key"],
        )

        if close_result:
            refunded = close_result.get("totalRefunded", "0")
            total = close_result.get("totalSpent", "0")
            count = close_result.get("requestCount", 0)
            self.log("[ok]", f"Session closed | Spent: ${total} | Refunded: ${refunded} | Requests: {count}")
        else:
            self.log("!", "Session close failed")

        self.log("  ", "")
        self.log("[ok]", "Gateway flow: session -> proxy -> settle -> close (complete)")

    # ── Phase 3: Reputation Building via Gateway ─────────────────────────

    def build_reputation(self):
        """Build reputation by running proxy calls through the gateway."""
        self.log("  ", "Building agent reputation via gateway proxy calls...")

        # Define reputation-building scenarios
        # Each tuple: (buyer_name, service_type, params, count)
        scenarios = [
            # TradingAgent buys a lot (-> trusted/elite tier)
            ("TradingAgent", "inference", {"text": "market data", "task": "analyze"}, 8),
            ("TradingAgent", "translation", {"text": "report", "target": "es"}, 3),
            ("TradingAgent", "code-review", {"code": "x = 1"}, 3),
            ("TradingAgent", "embedding", {"text": "vectors"}, 2),
            # TranslatorBot buys some (-> established tier)
            ("TranslatorBot", "inference", {"text": "research topic", "task": "summarize"}, 5),
            ("TranslatorBot", "code-review", {"code": "pass"}, 3),
            ("TranslatorBot", "embedding", {"text": "embed this"}, 2),
            # CodeReviewBot buys a few (-> emerging tier)
            ("CodeReviewBot", "inference", {"text": "analyze code quality", "task": "analyze"}, 3),
            ("CodeReviewBot", "translation", {"text": "docs", "target": "fr"}, 2),
            # Cross-agent traffic
            ("ResearchAgent", "translation", {"text": "findings", "target": "de"}, 2),
            ("DataScraper", "code-review", {"code": "scrape()"}, 2),
            ("ContentWriter", "translation", {"text": "article", "target": "ja"}, 2),
            ("EmbeddingService", "inference", {"text": "compare embeddings", "task": "classify"}, 2),
        ]

        total_calls = 0
        success = 0

        for buyer_name, svc_type, params, count in scenarios:
            if buyer_name not in self.agents:
                continue

            buyer = self.agents[buyer_name]

            # Create a session for this burst
            session_result = self.api_call(
                "POST",
                "/v1/gateway/sessions",
                {
                    "maxTotal": "5.00",
                    "maxPerRequest": "0.50",
                    "strategy": "cheapest",
                },
                api_key=buyer["api_key"],
            )

            if not session_result:
                continue

            token = session_result.get("token", session_result.get("session", {}).get("id", ""))
            session_id = session_result.get("session", {}).get("id", "")

            for _ in range(count):
                result = self.api_call(
                    "POST",
                    "/v1/gateway/proxy",
                    {"serviceType": svc_type, "params": params},
                    api_key=buyer["api_key"],
                    extra_headers={"X-Gateway-Token": token},
                )
                total_calls += 1
                if result:
                    success += 1
                time.sleep(0.1)

            # Close session
            self.api_call("DELETE", f"/v1/gateway/sessions/{session_id}", api_key=buyer["api_key"])

        self.log("[ok]", f"Completed {success}/{total_calls} reputation-building proxy calls")

    def show_reputation_summary(self):
        """Print agent reputation scores using per-agent reputation lookups."""
        self.log("  ", "Reputation Summary")
        self.log("  ", "-" * 55)

        tier_emoji = {
            "elite": "[*]",
            "trusted": "[+]",
            "established": "[o]",
            "emerging": "[-]",
            "new": "[ ]",
        }

        # Use batch reputation endpoint
        addresses = [info["address"] for info in self.agents.values()]
        batch_data = self.api_call("POST", "/v1/reputation/batch", {"addresses": addresses})

        entries = []
        if batch_data and "results" in batch_data:
            for result in batch_data["results"]:
                addr = result.get("address", "")
                name = addr[:10] + "..."
                for aname, ainfo in self.agents.items():
                    if ainfo["address"].lower() == addr.lower():
                        name = aname
                        break
                score = result.get("score", 0)
                tier = result.get("tier", "new")
                txns = result.get("totalTransactions", 0)
                entries.append((score, name, tier, txns))
        else:
            # Fallback: query individually
            for aname, ainfo in self.agents.items():
                data = self.api_call("GET", f"/v1/reputation/{ainfo['address']}")
                if data:
                    score = data.get("score", 0)
                    tier = data.get("tier", "new")
                    txns = data.get("totalTransactions", 0)
                    entries.append((score, aname, tier, txns))

        # Sort by score descending
        entries.sort(key=lambda x: x[0], reverse=True)

        tier_counts = {"elite": 0, "trusted": 0, "established": 0, "emerging": 0, "new": 0}
        for score, name, tier, txns in entries:
            emoji = tier_emoji.get(tier, "[ ]")
            self.log("  ", f"  {emoji} {name:<20s}  {score:5.1f}  {tier:<12s}  {txns} txns")
            tier_counts[tier] = tier_counts.get(tier, 0) + 1

        self.log("  ", "")
        self.log("  ", "Tier Distribution:")
        self.log("  ", f"  [*] Elite:       {tier_counts.get('elite', 0)}")
        self.log("  ", f"  [+] Trusted:     {tier_counts.get('trusted', 0)}")
        self.log("  ", f"  [o] Established: {tier_counts.get('established', 0)}")
        self.log("  ", f"  [-] Emerging:    {tier_counts.get('emerging', 0)}")
        self.log("  ", f"  [ ] New:         {tier_counts.get('new', 0)}")

    # ── Phase 4: Bounded Autonomy (Session Keys) ────────────────────────

    def demonstrate_session_keys(self):
        """Show bounded autonomy via session keys."""
        self.log("  ", "BOUNDED AUTONOMY (Session Keys)")
        self.log("  ", "-" * 55)

        if "TradingAgent" not in self.agents:
            self.log("!", "TradingAgent not found, skipping")
            return

        agent = self.agents["TradingAgent"]
        address = agent["address"]
        api_key = agent["api_key"]
        session_key_addr = random_address()

        self.log("  ", f"  Agent: TradingAgent ({address[:10]}...)")
        self.log("  ", f"  Max per transaction: $0.50")
        self.log("  ", f"  Max per day: $5.00")
        self.log("  ", f"  Allowed services: inference, embedding only")
        self.log("  ", f"  Expires: 24 hours")

        result = self.api_call(
            "POST",
            f"/v1/agents/{address}/sessions",
            {
                "publicKey": session_key_addr,
                "maxPerTransaction": "0.50",
                "maxPerDay": "5.00",
                "maxTotal": "50.00",
                "allowedServiceTypes": ["inference", "embedding"],
                "expiresIn": "24h",
                "label": "demo-trading-key",
            },
            api_key=api_key,
        )

        if result:
            key_id = result.get("id", "unknown")
            self.log("[ok]", f"Session key created: {key_id}")
            self.log("  ", "")
            self.log("  ", "This is BOUNDED AUTONOMY:")
            self.log("  ", "  Agent CAN spend up to $0.50/tx on inference & embedding")
            self.log("  ", "  Agent CANNOT exceed $5/day")
            self.log("  ", "  Agent CANNOT pay for translation or code-review services")
            self.log("  ", "  Owner CAN revoke instantly if needed")
        else:
            self.log("!", "Session key creation failed")

        time.sleep(self.delays["phase"])

    # ── Phase 5: Multi-Agent Pipeline via Gateway ────────────────────────

    def demonstrate_pipeline(self):
        """Demonstrate a multi-step agent pipeline using gateway proxy."""
        self.log("  ", "MULTI-AGENT PIPELINE (via Gateway)")
        self.log("  ", "-" * 55)
        self.log("  ", "  Pipeline: Summarize -> Translate -> Generate Embedding")
        self.log("  ", "")

        buyer_name = "TradingAgent"
        if buyer_name not in self.agents:
            self.log("!", "TradingAgent not found, skipping pipeline demo")
            return

        buyer = self.agents[buyer_name]

        # Create a session for the pipeline
        session_result = self.api_call(
            "POST",
            "/v1/gateway/sessions",
            {
                "maxTotal": "1.00",
                "maxPerRequest": "0.20",
                "strategy": "cheapest",
            },
            api_key=buyer["api_key"],
        )

        if not session_result:
            self.log("!", "Failed to create pipeline session")
            return

        token = session_result.get("token", session_result.get("session", {}).get("id", ""))
        session_id = session_result.get("session", {}).get("id", "")

        steps = [
            {
                "name": "Summarize",
                "serviceType": "inference",
                "params": {"text": "The quarterly report shows strong growth in AI agent adoption, "
                                   "with transaction volumes up 340% quarter over quarter.", "task": "summarize"},
            },
            {
                "name": "Translate",
                "serviceType": "translation",
                "params": {"text": "AI agent payments grew 340% this quarter", "target": "es"},
            },
            {
                "name": "Embed",
                "serviceType": "embedding",
                "params": {"text": "AI agent payment growth quarterly report analysis"},
            },
        ]

        total_cost = 0.0

        for i, step in enumerate(steps, 1):
            result = self.api_call(
                "POST",
                "/v1/gateway/proxy",
                {
                    "serviceType": step["serviceType"],
                    "params": step["params"],
                },
                api_key=buyer["api_key"],
                extra_headers={"X-Gateway-Token": token},
            )

            if result:
                proxy_result = result.get("result", {})
                amount = proxy_result.get("amountPaid", "0")
                service = proxy_result.get("serviceName", "?")
                response = proxy_result.get("response", {})
                output = str(response.get("output", ""))[:55]
                total_cost += float(amount)
                self.log("  ", f"  [{i}/3] {step['name']:<12s} ${amount} via {service}")
                self.log("  ", f"         -> {output}...")
            else:
                self.log("  ", f"  [{i}/3] {step['name']:<12s} FAILED")

            time.sleep(self.delays["tx"])

        # Close session
        self.api_call("DELETE", f"/v1/gateway/sessions/{session_id}", api_key=buyer["api_key"])

        self.log("  ", "")
        self.log("[ok]", f"Pipeline complete. Total cost: ${total_cost:.4f}")

    # ── Phase 6: Escrow Protection ───────────────────────────────────────

    def demonstrate_escrow(self):
        """Demonstrate buyer-protected escrow payments."""
        self.log("  ", "ESCROW PROTECTION")
        self.log("  ", "-" * 55)

        buyer_name = "TradingAgent"
        seller_name = "ResearchAgent"

        if buyer_name not in self.agents or seller_name not in self.agents:
            self.log("!", "Required agents not found, skipping escrow demo")
            return

        buyer = self.agents[buyer_name]
        seller = self.agents[seller_name]
        amount = "0.15"

        self.log("  ", f"  {buyer_name} wants to buy Competitive Analysis from {seller_name}")
        self.log("  ", f"  Price: ${amount} USDC")
        self.log("  ", "")

        # Step 1: Create escrow
        self.log("  ", "  Creating escrow (funds held in smart contract)...")
        escrow_result = self.api_call(
            "POST",
            "/v1/escrow",
            {
                "buyerAddr": buyer["address"],
                "sellerAddr": seller["address"],
                "amount": amount,
                "autoRelease": "5m",
            },
            api_key=buyer["api_key"],
        )

        if not escrow_result:
            self.log("!", "Failed to create escrow (buyer may need sufficient balance)")
            self.log("  ", "  Simulating escrow flow instead...")
            self.log("  ", f"  -> Escrow created: ${amount} held")
            self.log("  ", f"  -> Seller delivers research report")
            self.log("  ", f"  -> Buyer confirms quality")
            self.log("  ", f"  -> Funds released to {seller_name}")
            self.log("  ", "")
            self.log("[ok]", "Escrow protects both parties in high-value transactions")
            return

        escrow = escrow_result.get("escrow", {})
        escrow_id = escrow.get("id", "unknown")
        self.log("[ok]", f"Escrow created: {escrow_id}")
        self.log("  ", f"  ${amount} held -- both parties protected")

        time.sleep(self.delays["phase"])

        # Step 2: Seller delivers
        self.log("  ", "")
        self.log("  ", f"  {seller_name} delivers research report...")
        deliver_result = self.api_call(
            "POST",
            f"/v1/escrow/{escrow_id}/deliver",
            {},
            api_key=seller["api_key"],
        )

        if deliver_result:
            self.log("[ok]", "Delivery marked -- buyer can now review")
        else:
            self.log("!", "Delivery marking failed")

        time.sleep(self.delays["phase"])

        # Step 3: Buyer confirms
        self.log("  ", f"  {buyer_name} confirms quality...")
        confirm_result = self.api_call(
            "POST",
            f"/v1/escrow/{escrow_id}/confirm",
            {},
            api_key=buyer["api_key"],
        )

        if confirm_result:
            self.log("[ok]", f"Funds released to {seller_name}!")
        else:
            self.log("!", "Confirmation failed")

        self.log("  ", "")
        self.log("[ok]", f"Escrow lifecycle: created -> delivered -> confirmed -> released")

    # ── Phase 7: Live Gateway Simulation ────────────────────────────────

    def run_live_simulation(self, duration: Optional[int] = None):
        """Run live gateway proxy activity."""
        self.log("  ", "LIVE GATEWAY SIMULATION")
        self.log("  ", f"  Speed: {self.speed}")
        if duration:
            self.log("  ", f"  Auto-stop in {duration}s")
        else:
            self.log("  ", "  Press Ctrl+C to stop")
        self.log("  ", f"  Dashboard: {self.base_url}")
        self.log("  ", "")

        # Pick random buyers and service types for live traffic
        buyer_names = [n for n in self.agents.keys()]
        service_types = ["inference", "translation", "code-review", "embedding"]
        params_by_type = {
            "inference": [
                {"text": "analyze market trends", "task": "analyze"},
                {"text": "summarize research paper", "task": "summarize"},
                {"text": "classify document type", "task": "classify"},
            ],
            "translation": [
                {"text": "AI agent infrastructure", "target": "es"},
                {"text": "Payment settlement complete", "target": "fr"},
                {"text": "Decentralized compute network", "target": "de"},
            ],
            "code-review": [
                {"code": "def transfer(a, b, amt): ledger[a] -= amt; ledger[b] += amt"},
                {"code": "async def settle(): await confirm_payment()"},
            ],
            "embedding": [
                {"text": "agent payment infrastructure"},
                {"text": "decentralized AI marketplace"},
            ],
        }

        # Create a long-running session for live simulation
        active_sessions = {}  # buyer_name -> (session_id, token)
        for name in buyer_names:
            agent = self.agents[name]
            result = self.api_call(
                "POST",
                "/v1/gateway/sessions",
                {"maxTotal": "3.00", "maxPerRequest": "0.50", "strategy": "cheapest"},
                api_key=agent["api_key"],
            )
            if result:
                sid = result.get("session", {}).get("id", "")
                tok = result.get("token", sid)
                active_sessions[name] = (sid, tok)

        tx_count = 0
        start_time = time.time()

        try:
            while self.running:
                if duration and (time.time() - start_time) >= duration:
                    break

                # Pick a random buyer with an active session
                available = [n for n in buyer_names if n in active_sessions]
                if not available:
                    break

                buyer_name = random.choice(available)
                buyer = self.agents[buyer_name]
                session_id, token = active_sessions[buyer_name]
                svc_type = random.choice(service_types)
                params = random.choice(params_by_type[svc_type])

                result = self.api_call(
                    "POST",
                    "/v1/gateway/proxy",
                    {"serviceType": svc_type, "params": params},
                    api_key=buyer["api_key"],
                    extra_headers={"X-Gateway-Token": token},
                )

                if result:
                    proxy_result = result.get("result", {})
                    amount = proxy_result.get("amountPaid", "0")
                    service = proxy_result.get("serviceName", "?")
                    self.log("  ", f"{buyer_name} -> {service}: ${amount} ({svc_type})")
                    tx_count += 1

                    # Check if budget is low
                    if result.get("budgetLow"):
                        # Close and recreate session
                        self.api_call("DELETE", f"/v1/gateway/sessions/{session_id}",
                                      api_key=buyer["api_key"])
                        new_session = self.api_call(
                            "POST", "/v1/gateway/sessions",
                            {"maxTotal": "3.00", "maxPerRequest": "0.50", "strategy": "cheapest"},
                            api_key=buyer["api_key"],
                        )
                        if new_session:
                            sid = new_session.get("session", {}).get("id", "")
                            tok = new_session.get("token", sid)
                            active_sessions[buyer_name] = (sid, tok)
                        else:
                            del active_sessions[buyer_name]
                else:
                    # Session might be exhausted
                    self.api_call("DELETE", f"/v1/gateway/sessions/{session_id}",
                                  api_key=buyer["api_key"])
                    new_session = self.api_call(
                        "POST", "/v1/gateway/sessions",
                        {"maxTotal": "3.00", "maxPerRequest": "0.50", "strategy": "cheapest"},
                        api_key=buyer["api_key"],
                    )
                    if new_session:
                        sid = new_session.get("session", {}).get("id", "")
                        tok = new_session.get("token", sid)
                        active_sessions[buyer_name] = (sid, tok)
                    else:
                        del active_sessions[buyer_name]

                time.sleep(self.delays["tx"])

                # Stats every 10 transactions
                if tx_count % 10 == 0 and tx_count > 0:
                    stats = self.api_call("GET", "/v1/network/stats")
                    if stats:
                        volume = float(stats.get("totalVolume", 0) or 0)
                        txns = stats.get("totalTransactions", 0)
                        self.log("  ", f"Network: ${volume:.4f} volume, {txns} transactions")

        except KeyboardInterrupt:
            self.running = False

        # Clean up sessions
        for name, (sid, _) in active_sessions.items():
            self.api_call("DELETE", f"/v1/gateway/sessions/{sid}",
                          api_key=self.agents[name]["api_key"])

        self.log("  ", "")
        self.log("  ", f"Simulation ended ({tx_count} gateway proxy calls)")

    # ── Demo Summary ─────────────────────────────────────────────────────

    def print_summary(self):
        """Print final demo summary stats."""
        stats = self.api_call("GET", "/v1/network/stats")

        volume = "?"
        txns = "?"
        agents = len(self.agents)

        if stats:
            volume = f"${float(stats.get('totalVolume', 0) or 0):.2f}"
            txns = stats.get("totalTransactions", 0)

        print()
        print("=" * 60)
        print("  DEMO COMPLETE")
        print(f"  Volume: {volume} | Transactions: {txns} | Agents: {agents}")
        print(f"  All traffic flowed through the gateway proxy:")
        print(f"    discover -> hold -> forward -> settle -> receipt")
        print(f"  Dashboard: {self.base_url}")
        print("=" * 60)

    # ── Main Runner ──────────────────────────────────────────────────────

    def run(self):
        """Run the full gateway demo."""
        print()
        print("=" * 60)
        print("  ALANCOIN -- Economic Infrastructure for AI Agents")
        print("  Gateway Proxy Demo")
        print("=" * 60)
        print()

        # Start the stub service provider
        self.log("  ", "Starting stub service provider...")
        try:
            self.stub_provider.start()
            self.log("[ok]", f"Stub provider running on port {STUB_PROVIDER_PORT}")
        except Exception as e:
            self.log("!", f"Failed to start stub provider: {e}")
            self.log("!", "Run manually: python3 scripts/stub_provider.py")
            return
        print()

        # Phase 1: Network Bootstrap
        self.log("  ", "PHASE 1: NETWORK BOOTSTRAP")
        self.setup_agents()
        self.fund_agents()
        print()

        # Phase 2: Gateway Proxy (the core product flow)
        self.log("  ", "PHASE 2: GATEWAY PROXY FLOW")
        self.demonstrate_gateway_proxy()
        print()

        # Phase 3: Reputation Building
        self.log("  ", "PHASE 3: REPUTATION BUILDING (via Gateway)")
        self.build_reputation()
        print()
        self.show_reputation_summary()
        print()

        # Phase 4: Session Keys
        self.log("  ", "PHASE 4: BOUNDED AUTONOMY")
        self.demonstrate_session_keys()
        print()

        # Phase 5: Pipeline
        self.log("  ", "PHASE 5: MULTI-AGENT PIPELINE")
        self.demonstrate_pipeline()
        print()

        # Phase 6: Escrow
        self.log("  ", "PHASE 6: ESCROW PROTECTION")
        self.demonstrate_escrow()
        print()

        # Phase 7: Live Simulation
        self.log("  ", "PHASE 7: LIVE GATEWAY SIMULATION")
        if self.demo_mode:
            self.run_live_simulation(duration=15)
        else:
            self.run_live_simulation()

        self.print_summary()

        # Stop the stub provider
        self.stub_provider.stop()


def main():
    parser = argparse.ArgumentParser(description="Alancoin Gateway Demo")
    parser.add_argument("--url", default=BASE_URL, help="API base URL")
    parser.add_argument("--speed", choices=["fast", "normal", "slow"],
                        default="normal", help="Demo speed")
    parser.add_argument("--demo", action="store_true",
                        help="Demo mode: auto-stops after all phases complete")
    args = parser.parse_args()

    # Check server is running
    try:
        resp = requests.get(f"{args.url}/health/live", timeout=5)
        if resp.status_code != 200:
            print(f"Server not ready at {args.url}")
            print("Start with: make run")
            return
    except Exception:
        print(f"Cannot connect to {args.url}")
        print("Start the server with: make run")
        return

    demo = DemoRunner(args.url, args.speed, demo_mode=args.demo)

    # Ensure cleanup on exit
    def cleanup():
        demo.stub_provider.stop()
    atexit.register(cleanup)

    # Handle signals for clean shutdown
    def signal_handler(signum, frame):
        demo.running = False
        demo.stub_provider.stop()
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    demo.run()


if __name__ == "__main__":
    main()
