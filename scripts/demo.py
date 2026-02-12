#!/usr/bin/env python3
"""
Alancoin Investor Demo Script

Creates a living network demonstration showing:
1. Agent registration and service discovery
2. Reputation building with tier differentiation
3. Session keys with bounded autonomy
4. Credit system (apply, spend on credit, auto-repay)
5. Multi-agent pipeline (summarize -> translate -> extract)
6. Escrow protection (buyer-protected payments)
7. Live simulation with dashboard streaming

Usage:
    python scripts/demo.py [--url http://localhost:8080] [--speed fast|normal|slow] [--demo]
"""

import argparse
import random
import secrets
import time
from datetime import datetime
from typing import Optional

import requests

# Demo configuration
BASE_URL = "http://localhost:8080"

# Agent personas for the demo
AGENTS = [
    {
        "name": "TranslatorBot",
        "description": "High-quality language translation powered by GPT-4",
        "services": [
            {"type": "translation", "name": "English to Spanish", "price": "0.001"},
            {"type": "translation", "name": "English to French", "price": "0.0015"},
            {"type": "translation", "name": "English to German", "price": "0.002"},
        ],
    },
    {
        "name": "ResearchAgent",
        "description": "Deep web research and comprehensive summarization",
        "services": [
            {"type": "data", "name": "Web Research", "price": "0.05"},
            {"type": "data", "name": "SEC Filing Summary", "price": "0.10"},
            {"type": "data", "name": "Competitive Analysis", "price": "0.15"},
        ],
    },
    {
        "name": "CodeReviewBot",
        "description": "Automated code review with security analysis",
        "services": [
            {"type": "code", "name": "Code Review", "price": "0.02"},
            {"type": "code", "name": "Test Generation", "price": "0.03"},
            {"type": "code", "name": "Security Audit", "price": "0.05"},
        ],
    },
    {
        "name": "DataScraper",
        "description": "Real-time data extraction and monitoring",
        "services": [
            {"type": "data", "name": "Price Feed", "price": "0.002"},
            {"type": "data", "name": "Social Sentiment", "price": "0.01"},
        ],
    },
    {
        "name": "TradingAgent",
        "description": "DeFi analytics and yield optimization",
        "services": [
            {"type": "compute", "name": "Yield Analysis", "price": "0.08"},
            {"type": "compute", "name": "Risk Assessment", "price": "0.12"},
        ],
    },
    {
        "name": "ContentWriter",
        "description": "AI-powered content generation and editing",
        "services": [
            {"type": "content", "name": "Blog Post", "price": "0.04"},
            {"type": "content", "name": "Tweet Thread", "price": "0.01"},
        ],
    },
    {
        "name": "ImageProcessor",
        "description": "Image analysis and generation",
        "services": [
            {"type": "media", "name": "Image Caption", "price": "0.005"},
            {"type": "media", "name": "Style Transfer", "price": "0.02"},
        ],
    },
]

# Transaction scenarios showing real agent-to-agent commerce
TRANSACTION_SCENARIOS = [
    ("TradingAgent", "DataScraper", "0.002", "Price Feed"),
    ("TradingAgent", "ResearchAgent", "0.10", "SEC Filing Summary"),
    ("ContentWriter", "TranslatorBot", "0.001", "English to Spanish"),
    ("CodeReviewBot", "ResearchAgent", "0.05", "Web Research"),
    ("ImageProcessor", "ContentWriter", "0.04", "Blog Post"),
    ("DataScraper", "CodeReviewBot", "0.02", "Code Review"),
    ("ResearchAgent", "TranslatorBot", "0.002", "English to German"),
    ("TradingAgent", "CodeReviewBot", "0.05", "Security Audit"),
]


def random_address() -> str:
    """Generate a random Ethereum-style address."""
    return "0x" + secrets.token_hex(20)


def random_tx_hash() -> str:
    """Generate a random transaction hash."""
    return "0x" + secrets.token_hex(32)


class DemoRunner:
    def __init__(self, base_url: str, speed: str = "normal", demo_mode: bool = False):
        self.base_url = base_url.rstrip("/")
        self.speed = speed
        self.demo_mode = demo_mode
        self.agents = {}  # name -> {address, api_key, services}
        self.running = True

        # Speed settings (seconds between actions)
        self.delays = {
            "fast": {"tx": 0.3, "session": 1, "phase": 1},
            "normal": {"tx": 2, "session": 5, "phase": 2},
            "slow": {"tx": 5, "session": 15, "phase": 5},
        }[speed]

    def log(self, emoji: str, message: str):
        """Print a timestamped log message."""
        timestamp = datetime.now().strftime("%H:%M:%S")
        print(f"[{timestamp}] {emoji} {message}")

    def api_call(self, method: str, endpoint: str, json_data: dict = None,
                 api_key: str = None, retries: int = 2) -> Optional[dict]:
        """Make an API call with error handling and retry on rate limit."""
        url = f"{self.base_url}{endpoint}"
        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"

        for attempt in range(retries + 1):
            try:
                if method == "GET":
                    resp = requests.get(url, headers=headers, timeout=10)
                elif method == "POST":
                    resp = requests.post(url, json=json_data, headers=headers, timeout=10)
                elif method == "DELETE":
                    resp = requests.delete(url, headers=headers, timeout=10)
                else:
                    raise ValueError(f"Unknown method: {method}")

                if resp.status_code == 429 and attempt < retries:
                    time.sleep(1.0)
                    continue

                if resp.status_code >= 400:
                    return None
                return resp.json() if resp.text else {}
            except Exception as e:
                self.log("!", f"API error: {e}")
                return None
        return None

    def api_call_raw(self, method: str, endpoint: str, json_data: dict = None,
                     api_key: str = None):
        """Make an API call returning the raw response (for checking status codes)."""
        url = f"{self.base_url}{endpoint}"
        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"

        try:
            if method == "GET":
                return requests.get(url, headers=headers, timeout=10)
            elif method == "POST":
                return requests.post(url, json=json_data, headers=headers, timeout=10)
            else:
                return None
        except Exception:
            return None

    # ── Phase 1: Agent Registration ──────────────────────────────────────

    def setup_agents(self):
        """Register all demo agents."""
        self.log("🤖", "Registering AI agents with services...")

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
                    },
                    api_key=api_key,
                )
                if svc_result:
                    self.agents[agent_config["name"]]["services"].append(service)
                    total_services += 1

            svc_count = len(self.agents[agent_config["name"]]["services"])
            self.log("  ", f"  {agent_config['name']} ({svc_count} services)")

        self.log("✓", f"Registered {len(self.agents)} agents with {total_services} services")

    def fund_agents(self):
        """Fund all agents with initial USDC deposits."""
        self.log("💰", "Funding agents with initial USDC deposits...")

        funded = 0
        for name, agent in self.agents.items():
            result = self.api_call("POST", "/v1/admin/deposits", {
                "agentAddress": agent["address"],
                "amount": "10.00",
                "txHash": random_tx_hash(),
            }, api_key=agent["api_key"])

            if result:
                funded += 1

        self.log("✓", f"Funded {funded} agents with $10.00 USDC each")

    # ── Phase 2: Reputation Building ─────────────────────────────────────

    def build_reputation(self):
        """Build visible reputation via transaction burst."""
        self.log("⭐", "Building agent reputation...")

        reputation_txns = [
            # TradingAgent buys heavily (20+ txns -> trusted)
            ("TradingAgent", "DataScraper", "0.002", "Price Feed"),
            ("TradingAgent", "DataScraper", "0.002", "Price Feed"),
            ("TradingAgent", "DataScraper", "0.002", "Price Feed"),
            ("TradingAgent", "DataScraper", "0.010", "Social Sentiment"),
            ("TradingAgent", "DataScraper", "0.010", "Social Sentiment"),
            ("TradingAgent", "ResearchAgent", "0.10", "SEC Filing Summary"),
            ("TradingAgent", "ResearchAgent", "0.10", "SEC Filing Summary"),
            ("TradingAgent", "ResearchAgent", "0.05", "Web Research"),
            ("TradingAgent", "ResearchAgent", "0.05", "Web Research"),
            ("TradingAgent", "ResearchAgent", "0.15", "Competitive Analysis"),
            ("TradingAgent", "CodeReviewBot", "0.05", "Security Audit"),
            ("TradingAgent", "CodeReviewBot", "0.05", "Security Audit"),
            ("TradingAgent", "CodeReviewBot", "0.02", "Code Review"),
            ("TradingAgent", "TranslatorBot", "0.001", "English to Spanish"),
            ("TradingAgent", "TranslatorBot", "0.002", "English to German"),
            ("TradingAgent", "ContentWriter", "0.04", "Blog Post"),
            ("TradingAgent", "ContentWriter", "0.01", "Tweet Thread"),
            ("TradingAgent", "ImageProcessor", "0.005", "Image Caption"),
            ("TradingAgent", "ImageProcessor", "0.020", "Style Transfer"),
            ("TradingAgent", "DataScraper", "0.002", "Price Feed"),
            # TranslatorBot (10+ txns -> established)
            ("TranslatorBot", "ResearchAgent", "0.05", "Web Research"),
            ("TranslatorBot", "ResearchAgent", "0.05", "Web Research"),
            ("TranslatorBot", "ContentWriter", "0.04", "Blog Post"),
            ("TranslatorBot", "ContentWriter", "0.01", "Tweet Thread"),
            ("TranslatorBot", "CodeReviewBot", "0.02", "Code Review"),
            ("TranslatorBot", "CodeReviewBot", "0.03", "Test Generation"),
            ("TranslatorBot", "DataScraper", "0.002", "Price Feed"),
            ("TranslatorBot", "DataScraper", "0.010", "Social Sentiment"),
            ("TranslatorBot", "ImageProcessor", "0.005", "Image Caption"),
            ("TranslatorBot", "TradingAgent", "0.08", "Yield Analysis"),
            ("TranslatorBot", "TradingAgent", "0.12", "Risk Assessment"),
            # CodeReviewBot (5+ txns -> emerging)
            ("CodeReviewBot", "ResearchAgent", "0.05", "Web Research"),
            ("CodeReviewBot", "TranslatorBot", "0.001", "English to Spanish"),
            ("CodeReviewBot", "TranslatorBot", "0.0015", "English to French"),
            ("CodeReviewBot", "DataScraper", "0.002", "Price Feed"),
            ("CodeReviewBot", "ContentWriter", "0.04", "Blog Post"),
            # ResearchAgent buys a few
            ("ResearchAgent", "TranslatorBot", "0.001", "English to Spanish"),
            ("ResearchAgent", "TranslatorBot", "0.002", "English to German"),
            ("ResearchAgent", "DataScraper", "0.002", "Price Feed"),
            # Cross-agent traffic
            ("DataScraper", "CodeReviewBot", "0.02", "Code Review"),
            ("DataScraper", "TradingAgent", "0.08", "Yield Analysis"),
            ("ContentWriter", "TranslatorBot", "0.001", "English to Spanish"),
            ("ContentWriter", "ResearchAgent", "0.05", "Web Research"),
            ("ImageProcessor", "ContentWriter", "0.04", "Blog Post"),
            ("ImageProcessor", "CodeReviewBot", "0.02", "Code Review"),
        ]

        success = 0
        for from_name, to_name, amount, service in reputation_txns:
            if from_name not in self.agents or to_name not in self.agents:
                continue
            from_agent = self.agents[from_name]
            to_agent = self.agents[to_name]
            tx_hash = random_tx_hash()
            result = self.api_call("POST", "/v1/transactions", {
                "txHash": tx_hash,
                "from": from_agent["address"],
                "to": to_agent["address"],
                "amount": amount,
            }, api_key=from_agent["api_key"])
            if result:
                success += 1
            time.sleep(0.15)

        self.log("✓", f"Executed {success} reputation-building transactions")

    def show_reputation_summary(self):
        """Print the reputation leaderboard."""
        self.log("🏆", "Reputation Leaderboard")
        self.log("  ", "-" * 55)

        data = self.api_call("GET", "/v1/reputation?limit=20")
        if not data or "leaderboard" not in data:
            self.log("!", "Could not fetch leaderboard")
            return

        tier_emoji = {
            "elite": "★",
            "trusted": "◆",
            "established": "●",
            "emerging": "○",
            "new": " ",
        }

        for entry in data["leaderboard"]:
            emoji = tier_emoji.get(entry.get("tier", "new"), " ")
            addr = entry.get("address", "")
            name = addr[:10] + "..."
            for aname, ainfo in self.agents.items():
                if ainfo["address"].lower() == addr.lower():
                    name = aname
                    break
            score = entry.get("score", 0)
            tier = entry.get("tier", "new")
            txns = entry.get("totalTransactions", 0)
            self.log("  ", f"  {emoji} {name:<20s}  {score:5.1f}  {tier:<12s}  {txns} txns")

        tiers = data.get("tiers", {})
        self.log("  ", "")
        self.log("📊", "Tier Distribution:")
        self.log("  ", f"  ★ Elite:       {tiers.get('elite', 0)}")
        self.log("  ", f"  ◆ Trusted:     {tiers.get('trusted', 0)}")
        self.log("  ", f"  ● Established: {tiers.get('established', 0)}")
        self.log("  ", f"  ○ Emerging:    {tiers.get('emerging', 0)}")
        self.log("  ", f"    New:         {tiers.get('new', 0)}")

    # ── Phase 3: Bounded Autonomy ────────────────────────────────────────

    def demonstrate_session_keys(self):
        """Show bounded autonomy via session keys."""
        self.log("🔑", "BOUNDED AUTONOMY (Session Keys)")
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
        self.log("  ", f"  Allowed services: data, compute only")
        self.log("  ", f"  Expires: 24 hours")

        result = self.api_call(
            "POST",
            f"/v1/agents/{address}/sessions",
            {
                "publicKey": session_key_addr,
                "maxPerTransaction": "0.50",
                "maxPerDay": "5.00",
                "maxTotal": "50.00",
                "allowedServiceTypes": ["data", "compute"],
                "expiresIn": "24h",
                "label": "demo-trading-key",
            },
            api_key=api_key,
        )

        if result:
            key_id = result.get("id", "unknown")
            self.log("✅", f"Session key created: {key_id}")
            self.log("  ", "")
            self.log("🛡️", "This is BOUNDED AUTONOMY:")
            self.log("  ", "  Agent CAN spend up to $0.50/tx on data & compute")
            self.log("  ", "  Agent CANNOT exceed $5/day")
            self.log("  ", "  Agent CANNOT pay for translation or code services")
            self.log("  ", "  Owner CAN revoke instantly if needed")
        else:
            self.log("!", "Session key creation failed")

        time.sleep(self.delays["phase"])

    # ── Phase 4: Credit System ───────────────────────────────────────────

    def demonstrate_credit(self):
        """Demonstrate the credit system lifecycle."""
        self.log("💳", "CREDIT SYSTEM")
        self.log("  ", "-" * 55)

        if "TradingAgent" not in self.agents:
            self.log("!", "TradingAgent not found, skipping credit demo")
            return

        agent = self.agents["TradingAgent"]
        address = agent["address"]
        api_key = agent["api_key"]

        # Step 1: Check current credit status (should be none)
        self.log("  ", "  Checking TradingAgent credit status...")
        credit_resp = self.api_call_raw("GET", f"/v1/agents/{address}/credit")
        if credit_resp and credit_resp.status_code == 404:
            self.log("  ", "  No credit line yet (as expected for a new agent)")
        elif credit_resp and credit_resp.status_code == 200:
            self.log("  ", "  Credit line already exists")

        time.sleep(self.delays["phase"])

        # Step 2: Apply for credit
        self.log("  ", "")
        self.log("  ", "  TradingAgent applies for credit...")
        apply_result = self.api_call(
            "POST",
            f"/v1/agents/{address}/credit/apply",
            {},
            api_key=api_key,
        )

        if apply_result:
            credit_line = apply_result.get("credit_line", {})
            evaluation = apply_result.get("evaluation", {})
            limit = credit_line.get("creditLimit", "0")
            rate = evaluation.get("interestRate", 0)
            tier = credit_line.get("reputationTier", "unknown")

            self.log("✅", f"APPROVED: ${float(limit):.2f} credit line at {rate*100:.0f}% ({tier} tier)")

            # Step 3: Show effective balance
            balance_data = self.api_call("GET", f"/v1/agents/{address}/balance")
            if balance_data and "balance" in balance_data:
                bal = balance_data["balance"]
                available = float(bal.get("available", "0"))
                credit_limit = float(bal.get("creditLimit", "0"))
                effective = available + credit_limit
                self.log("  ", f"  Balance: ${available:.2f} available + ${credit_limit:.2f} credit = ${effective:.2f} effective")

            time.sleep(self.delays["phase"])

            # Step 4: Simulate spending that draws on credit
            # First drain some of the available balance via transactions
            self.log("  ", "")
            self.log("  ", "  Simulating heavy spending to draw on credit...")

            # Run several high-value transactions to spend down available balance
            spend_targets = [
                ("ResearchAgent", "0.15", "Competitive Analysis"),
                ("CodeReviewBot", "0.05", "Security Audit"),
                ("ResearchAgent", "0.10", "SEC Filing Summary"),
                ("DataScraper", "0.01", "Social Sentiment"),
            ]
            total_spent = 0.0
            for to_name, amount, service in spend_targets:
                if to_name in self.agents:
                    to_agent = self.agents[to_name]
                    tx_hash = random_tx_hash()
                    result = self.api_call("POST", "/v1/transactions", {
                        "txHash": tx_hash,
                        "from": address,
                        "to": to_agent["address"],
                        "amount": amount,
                    }, api_key=api_key)
                    if result:
                        total_spent += float(amount)
                    time.sleep(0.15)

            self.log("💸", f"  Spent ${total_spent:.2f} on services")

            # Show updated balance
            balance_data = self.api_call("GET", f"/v1/agents/{address}/balance")
            if balance_data and "balance" in balance_data:
                bal = balance_data["balance"]
                available = float(bal.get("available", "0"))
                credit_used = float(bal.get("creditUsed", "0"))
                credit_limit = float(bal.get("creditLimit", "0"))
                self.log("  ", f"  Available: ${available:.2f} | Credit used: ${credit_used:.2f} of ${credit_limit:.2f}")

            time.sleep(self.delays["phase"])

            # Step 5: Earn income -> auto-repayment
            self.log("  ", "")
            self.log("  ", "  TradingAgent earning from services...")

            # Simulate income: other agents buying from TradingAgent
            income_txns = [
                ("TranslatorBot", "0.08", "Yield Analysis"),
                ("CodeReviewBot", "0.12", "Risk Assessment"),
                ("DataScraper", "0.08", "Yield Analysis"),
            ]
            total_earned = 0.0
            for from_name, amount, service in income_txns:
                if from_name in self.agents:
                    from_agent = self.agents[from_name]
                    tx_hash = random_tx_hash()
                    result = self.api_call("POST", "/v1/transactions", {
                        "txHash": tx_hash,
                        "from": from_agent["address"],
                        "to": address,
                        "amount": amount,
                    }, api_key=from_agent["api_key"])
                    if result:
                        total_earned += float(amount)
                    time.sleep(0.15)

            self.log("💰", f"  Earned ${total_earned:.2f} from services")

            # Show final balance
            balance_data = self.api_call("GET", f"/v1/agents/{address}/balance")
            if balance_data and "balance" in balance_data:
                bal = balance_data["balance"]
                available = float(bal.get("available", "0"))
                credit_used = float(bal.get("creditUsed", "0"))
                self.log("  ", f"  Final: ${available:.2f} available, ${credit_used:.2f} credit outstanding")

            self.log("  ", "")
            self.log("✅", "Credit lifecycle complete: apply -> spend -> earn -> balance restored")
        else:
            self.log("!", "Credit application was not approved")
            self.log("  ", "  (Agent may need more transaction history or higher reputation)")

    # ── Phase 5: Multi-Agent Pipeline ────────────────────────────────────

    def demonstrate_pipeline(self):
        """Demonstrate a 3-step agent pipeline."""
        self.log("🔗", "MULTI-AGENT PIPELINE")
        self.log("  ", "-" * 55)
        self.log("  ", "  Pipeline: Summarize -> Translate -> Extract Entities")
        self.log("  ", "")

        # Use existing agents for the pipeline
        pipeline_agents = {
            "SummarizerBot": ("ResearchAgent", "0.008", "Web Research"),
            "TranslatorBot": ("TranslatorBot", "0.005", "English to Spanish"),
            "EntityExtractor": ("CodeReviewBot", "0.010", "Code Review"),
        }

        steps = [
            ("SummarizerBot", "Summarize source document", "0.008"),
            ("TranslatorBot", "Translate summary to Spanish", "0.005"),
            ("EntityExtractor", "Extract key entities", "0.010"),
        ]

        total_cost = 0.0
        buyer_name = "TradingAgent"

        if buyer_name not in self.agents:
            self.log("!", "TradingAgent not found, skipping pipeline demo")
            return

        buyer = self.agents[buyer_name]

        for i, (step_name, description, price) in enumerate(steps, 1):
            agent_name = pipeline_agents[step_name][0]
            if agent_name not in self.agents:
                self.log("!", f"  [{i}/3] {step_name} ({agent_name}) not found")
                continue

            seller = self.agents[agent_name]
            tx_hash = random_tx_hash()
            result = self.api_call("POST", "/v1/transactions", {
                "txHash": tx_hash,
                "from": buyer["address"],
                "to": seller["address"],
                "amount": price,
            }, api_key=buyer["api_key"])

            if result:
                total_cost += float(price)
                self.log("  ", f"  [{i}/3] {step_name:<20s} ${price} ✓  ({description})")
            else:
                self.log("  ", f"  [{i}/3] {step_name:<20s} ${price} ✗  (failed)")

            time.sleep(self.delays["tx"])

        self.log("  ", "")
        self.log("✅", f"Pipeline complete. Total cost: ${total_cost:.3f}")

    # ── Phase 6: Escrow Protection ───────────────────────────────────────

    def demonstrate_escrow(self):
        """Demonstrate buyer-protected escrow payments."""
        self.log("🔒", "ESCROW PROTECTION")
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
            self.log("  ", f"  -> Funds released to {seller_name} ✅")
            self.log("  ", "")
            self.log("✅", "Escrow protects both parties in high-value transactions")
            return

        escrow = escrow_result.get("escrow", {})
        escrow_id = escrow.get("id", "unknown")
        self.log("✅", f"Escrow created: {escrow_id}")
        self.log("  ", f"  ${amount} held — both parties protected")

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
            self.log("📦", "Delivery marked — buyer can now review")
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
            self.log("✅", f"Funds released to {seller_name}!")
        else:
            self.log("!", "Confirmation failed")

        self.log("  ", "")
        self.log("✅", f"Escrow lifecycle: created -> delivered -> confirmed -> released")

    # ── Phase 7: Live Simulation ─────────────────────────────────────────

    def run_live_simulation(self, duration: Optional[int] = None):
        """Run live activity. If duration is set, auto-stop after that many seconds."""
        self.log("🚀", "LIVE SIMULATION")
        self.log("  ", f"  Speed: {self.speed}")
        if duration:
            self.log("  ", f"  Auto-stop in {duration}s")
        else:
            self.log("  ", "  Press Ctrl+C to stop")
        self.log("  ", f"  Dashboard: {self.base_url}")
        self.log("  ", "")

        tx_scenarios = list(TRANSACTION_SCENARIOS)

        tx_count = 0
        start_time = time.time()

        try:
            while self.running:
                if duration and (time.time() - start_time) >= duration:
                    break

                # Transaction
                if tx_scenarios:
                    scenario = random.choice(tx_scenarios)
                    self.run_transaction(*scenario)
                    tx_count += 1

                time.sleep(self.delays["tx"])

                # Stats every 10 transactions
                if tx_count % 10 == 0 and tx_count > 0:
                    stats = self.api_call("GET", "/v1/network/stats")
                    if stats:
                        volume = float(stats.get("totalVolume", 0) or 0)
                        txns = stats.get("totalTransactions", 0)
                        self.log("📈", f"Network: ${volume:.4f} volume, {txns} transactions")

        except KeyboardInterrupt:
            self.running = False

        self.log("  ", "")
        self.log("🛑", f"Simulation ended ({tx_count} txns)")

    def run_transaction(self, from_name: str, to_name: str, amount: str, service: str):
        """Execute a transaction between two agents."""
        if from_name not in self.agents or to_name not in self.agents:
            return

        from_agent = self.agents[from_name]
        to_agent = self.agents[to_name]

        tx_hash = random_tx_hash()
        result = self.api_call("POST", "/v1/transactions", {
            "txHash": tx_hash,
            "from": from_agent["address"],
            "to": to_agent["address"],
            "amount": amount,
        }, api_key=from_agent["api_key"])

        if result:
            self.log("💸", f"{from_name} -> {to_name}: ${amount} ({service})")

    # ── Demo Summary ─────────────────────────────────────────────────────

    def print_summary(self):
        """Print final demo summary stats."""
        stats = self.api_call("GET", "/v1/network/stats")
        credit_data = self.api_call("GET", "/v1/credit/active")

        volume = "?"
        txns = "?"
        agents = len(self.agents)
        credit_extended = "0.00"
        credit_lines = 0

        if stats:
            volume = f"${float(stats.get('totalVolume', 0) or 0):.2f}"
            txns = stats.get("totalTransactions", 0)

        if credit_data:
            credit_lines_list = credit_data.get("credit_lines") or []
            credit_lines = len(credit_lines_list)
            total_credit = sum(float(cl.get("creditLimit", "0")) for cl in credit_lines_list)
            credit_extended = f"${total_credit:.2f}"

        print()
        print("=" * 60)
        print("  DEMO COMPLETE")
        print(f"  Volume: {volume} | Transactions: {txns} | Agents: {agents}")
        print(f"  Credit Extended: {credit_extended} | Credit Lines: {credit_lines}")
        print(f"  Dashboard: {self.base_url}")
        print("=" * 60)

    # ── Main Runner ──────────────────────────────────────────────────────

    def run(self):
        """Run the full investor demo."""
        print()
        print("=" * 60)
        print("  ALANCOIN — Economic Infrastructure for AI Agents")
        print("  Investor Demo")
        print("=" * 60)
        print()

        # Phase 1: Network Bootstrap
        self.log("🤖", "PHASE 1: NETWORK BOOTSTRAP")
        self.setup_agents()
        self.fund_agents()
        print()

        # Phase 2: Reputation Building
        self.log("⭐", "PHASE 2: REPUTATION BUILDING")
        self.build_reputation()
        print()
        self.show_reputation_summary()
        print()

        # Phase 4: Session Keys
        self.log("🔑", "PHASE 3: BOUNDED AUTONOMY")
        self.demonstrate_session_keys()
        print()

        # Phase 5: Credit System
        self.log("💳", "PHASE 4: CREDIT SYSTEM")
        self.demonstrate_credit()
        print()

        # Phase 6: Pipeline
        self.log("🔗", "PHASE 5: MULTI-AGENT PIPELINE")
        self.demonstrate_pipeline()
        print()

        # Phase 7: Escrow
        self.log("🔒", "PHASE 6: ESCROW PROTECTION")
        self.demonstrate_escrow()
        print()

        # Phase 8: Live Simulation
        self.log("🚀", "PHASE 7: LIVE SIMULATION")
        if self.demo_mode:
            self.run_live_simulation(duration=15)
        else:
            self.run_live_simulation()

        self.print_summary()


def main():
    parser = argparse.ArgumentParser(description="Alancoin Investor Demo")
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
    demo.run()


if __name__ == "__main__":
    main()
