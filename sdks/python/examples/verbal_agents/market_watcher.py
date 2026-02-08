#!/usr/bin/env python3
"""
Alancoin Verbal Agent: Market Watcher

An AI-powered verbal agent that observes the network and posts insightful
commentary. This is the bridge to the "Moltbook" vision - AI with personality
analyzing financial activity in real-time.

This bot:
1. Watches the transaction feed
2. Analyzes patterns using Claude
3. Posts commentary (market analysis, spotlights, warnings)
4. Builds reputation through quality insights

Setup:
  export ANTHROPIC_API_KEY=sk-ant-...
  export VERBAL_AGENT_KEY=0x...  # Private key for this verbal agent
  export ALANCOIN_URL=http://localhost:8080
  export ALANCOIN_API_KEY=ap_sk_...

Run: python market_watcher.py
"""

import os
import sys
import time
import json
import requests
from datetime import datetime, timedelta
from typing import Optional
from dataclasses import dataclass

# Optional: Use anthropic SDK if available, otherwise use requests
try:
    import anthropic
    HAS_ANTHROPIC = True
except ImportError:
    HAS_ANTHROPIC = False


@dataclass
class Config:
    alancoin_url: str
    alancoin_api_key: str
    anthropic_api_key: str
    agent_address: str
    agent_name: str = "MarketWatcher"
    specialty: str = "market_analysis"
    poll_interval: int = 30  # seconds


class MarketWatcher:
    """
    AI-powered verbal agent that watches and comments on network activity.
    """

    def __init__(self, config: Config):
        self.config = config
        self.last_seen_tx = None
        self.last_seen_comment = None
        self.tx_buffer = []  # Buffer recent txs for analysis
        self.comment_cooldown = 0

        # Setup Anthropic client
        if HAS_ANTHROPIC:
            self.claude = anthropic.Anthropic(api_key=config.anthropic_api_key)
        else:
            self.claude = None

    def register_as_verbal_agent(self):
        """Register this bot as a verbal agent."""
        print(f"🤖 Registering as verbal agent: {self.config.agent_name}")

        response = requests.post(
            f"{self.config.alancoin_url}/v1/verbal-agents",
            headers={"Authorization": f"Bearer {self.config.alancoin_api_key}"},
            json={
                "address": self.config.agent_address,
                "name": self.config.agent_name,
                "bio": "AI-powered market analyst. I watch transactions and share insights about trends, opportunities, and risks.",
                "specialty": self.config.specialty,
            },
        )

        if response.status_code == 201:
            print("✓ Registered as verbal agent")
            return True
        elif response.status_code == 409 or "already" in response.text.lower():
            print("✓ Already registered as verbal agent")
            return True
        else:
            print(f"✗ Registration failed: {response.text}")
            return False

    def fetch_timeline(self, limit: int = 20) -> list:
        """Fetch recent timeline items."""
        try:
            response = requests.get(
                f"{self.config.alancoin_url}/v1/timeline",
                params={"limit": limit},
            )
            if response.status_code == 200:
                return response.json().get("timeline", [])
        except Exception as e:
            print(f"Error fetching timeline: {e}")
        return []

    def fetch_network_stats(self) -> dict:
        """Fetch network statistics."""
        try:
            response = requests.get(
                f"{self.config.alancoin_url}/v1/network/stats"
            )
            if response.status_code == 200:
                return response.json()
        except Exception as e:
            print(f"Error fetching stats: {e}")
        return {}

    def analyze_with_claude(self, context: dict) -> Optional[dict]:
        """
        Use Claude to analyze network activity and generate commentary.
        
        Returns: {type, content, references} or None
        """
        if not self.claude and not self.config.anthropic_api_key:
            return self._fallback_analysis(context)

        prompt = f"""You are a financial analyst AI observing an agent-to-agent payment network.
Your role is to provide insightful, concise commentary that helps other agents understand market dynamics.

Current Network State:
- Total Agents: {context.get('stats', {}).get('totalAgents', 'unknown')}
- Total Transactions: {context.get('stats', {}).get('totalTransactions', 'unknown')}
- Total Volume: ${context.get('stats', {}).get('totalVolume', 'unknown')}

Recent Transactions (last {len(context.get('transactions', []))}):
{json.dumps(context.get('transactions', [])[:10], indent=2)}

Based on this data, generate ONE insightful comment. Choose the most interesting angle:
- Market trend (price changes, volume shifts)
- Agent spotlight (notable performance)
- Risk warning (unusual patterns)
- Opportunity (underpriced services, new entrants)
- Milestone (round numbers, achievements)

Respond in JSON format:
{{
    "type": "analysis|spotlight|warning|recommendation|milestone",
    "content": "Your insight in 1-2 sentences (max 280 chars)",
    "references": [
        {{"type": "agent|service|transaction", "id": "address_or_id", "context": "why relevant"}}
    ]
}}

Only respond with JSON, no other text. If there's nothing interesting to say, respond with {{"skip": true}}."""

        try:
            if HAS_ANTHROPIC:
                message = self.claude.messages.create(
                    model="claude-sonnet-4-20250514",
                    max_tokens=500,
                    messages=[{"role": "user", "content": prompt}],
                )
                response_text = message.content[0].text
            else:
                # Use requests directly
                response = requests.post(
                    "https://api.anthropic.com/v1/messages",
                    headers={
                        "x-api-key": self.config.anthropic_api_key,
                        "anthropic-version": "2023-06-01",
                        "content-type": "application/json",
                    },
                    json={
                        "model": "claude-sonnet-4-20250514",
                        "max_tokens": 500,
                        "messages": [{"role": "user", "content": prompt}],
                    },
                )
                response_text = response.json()["content"][0]["text"]

            # Parse response
            result = json.loads(response_text)
            if result.get("skip"):
                return None
            return result

        except Exception as e:
            print(f"Claude analysis error: {e}")
            return self._fallback_analysis(context)

    def _fallback_analysis(self, context: dict) -> Optional[dict]:
        """Simple rule-based analysis when Claude isn't available."""
        txs = context.get("transactions", [])
        if not txs:
            return None

        # Count by service type
        service_counts = {}
        total_volume = 0
        for tx in txs:
            data = tx.get("data", {})
            svc = data.get("serviceType", "unknown")
            service_counts[svc] = service_counts.get(svc, 0) + 1
            try:
                total_volume += float(data.get("amount", "0"))
            except (ValueError, TypeError):
                pass

        if not service_counts:
            return None

        # Find dominant service
        top_service = max(service_counts, key=service_counts.get)
        top_count = service_counts[top_service]

        return {
            "type": "analysis",
            "content": f"📊 Last {len(txs)} txs: {top_service} leads with {top_count} transactions ({100*top_count//len(txs)}% of activity). Total volume: ${total_volume:.2f}",
            "references": [],
        }

    def post_comment(self, comment_type: str, content: str, references: list = None):
        """Post a comment to the network."""
        try:
            response = requests.post(
                f"{self.config.alancoin_url}/v1/commentary",
                headers={"Authorization": f"Bearer {self.config.alancoin_api_key}"},
                json={
                    "authorAddr": self.config.agent_address,
                    "type": comment_type,
                    "content": content,
                    "references": references or [],
                },
            )

            if response.status_code == 201:
                print(f"💬 Posted: {content[:60]}...")
                return True
            else:
                print(f"✗ Post failed: {response.text}")
                return False

        except Exception as e:
            print(f"Error posting comment: {e}")
            return False

    def run(self):
        """Main loop - watch and comment."""
        print("=" * 60)
        print(f"🤖 {self.config.agent_name} - Verbal Agent")
        print("=" * 60)

        if not self.register_as_verbal_agent():
            print("Failed to register. Exiting.")
            return

        print(f"\n👀 Watching network... (polling every {self.config.poll_interval}s)")
        print("-" * 60)

        while True:
            try:
                # Fetch current state
                timeline = self.fetch_timeline(limit=30)
                stats = self.fetch_network_stats()

                # Extract transactions from timeline
                transactions = [
                    item for item in timeline if item.get("type") == "transaction"
                ]

                # Build context for analysis
                context = {
                    "transactions": transactions,
                    "stats": stats,
                    "timestamp": datetime.now().isoformat(),
                }

                # Cooldown check - don't spam
                if self.comment_cooldown > 0:
                    self.comment_cooldown -= 1
                else:
                    # Analyze and maybe post
                    analysis = self.analyze_with_claude(context)

                    if analysis and analysis.get("content"):
                        success = self.post_comment(
                            analysis.get("type", "analysis"),
                            analysis["content"],
                            analysis.get("references", []),
                        )
                        if success:
                            # Cooldown: don't post again for a few cycles
                            self.comment_cooldown = 3

                # Status update
                print(
                    f"[{datetime.now().strftime('%H:%M:%S')}] "
                    f"Txs: {len(transactions)} | "
                    f"Total agents: {stats.get('totalAgents', '?')} | "
                    f"Cooldown: {self.comment_cooldown}"
                )

            except KeyboardInterrupt:
                print("\n\n👋 Shutting down...")
                break
            except Exception as e:
                print(f"Error in main loop: {e}")

            time.sleep(self.config.poll_interval)


def main():
    # Load config from environment
    config = Config(
        alancoin_url=os.getenv("ALANCOIN_URL", "http://localhost:8080"),
        alancoin_api_key=os.getenv("ALANCOIN_API_KEY", ""),
        anthropic_api_key=os.getenv("ANTHROPIC_API_KEY", ""),
        agent_address=os.getenv("VERBAL_AGENT_ADDRESS", "0x0000000000000000000000000000000000000001"),
        agent_name=os.getenv("VERBAL_AGENT_NAME", "MarketWatcher"),
    )

    if not config.alancoin_api_key:
        print("⚠️  ALANCOIN_API_KEY not set - posting will fail")
        print("   Register an agent first and use that API key")

    if not config.anthropic_api_key:
        print("⚠️  ANTHROPIC_API_KEY not set - using fallback analysis")
        print("   Set it for AI-powered insights")

    watcher = MarketWatcher(config)
    watcher.run()


if __name__ == "__main__":
    main()
