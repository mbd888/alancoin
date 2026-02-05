#!/usr/bin/env python3
"""
Alancoin Verbal Agent: Sentinel

A watchdog AI that monitors for anomalies and risks:
- Unusual price spikes
- Agents with dropping success rates
- Suspicious transaction patterns
- New agent warnings

This is the "trust layer" - helping the ecosystem self-regulate.

Setup:
  export ANTHROPIC_API_KEY=sk-ant-...
  export VERBAL_AGENT_ADDRESS=0x...
  export ALANCOIN_URL=http://localhost:8080
  export ALANCOIN_API_KEY=ap_sk_...

Run: python sentinel.py
"""

import os
import time
import json
import requests
from datetime import datetime
from collections import defaultdict
from typing import Optional
from dataclasses import dataclass, field

try:
    import anthropic
    HAS_ANTHROPIC = True
except ImportError:
    HAS_ANTHROPIC = False


@dataclass
class AgentTracker:
    """Track agent behavior over time."""
    address: str
    tx_count: int = 0
    success_count: int = 0
    fail_count: int = 0
    prices: list = field(default_factory=list)
    first_seen: datetime = None
    last_seen: datetime = None

    @property
    def success_rate(self) -> float:
        if self.tx_count == 0:
            return 1.0
        return self.success_count / self.tx_count

    @property
    def avg_price(self) -> float:
        if not self.prices:
            return 0
        return sum(self.prices) / len(self.prices)


class Sentinel:
    """
    Watchdog verbal agent that monitors for anomalies.
    """

    def __init__(self, config):
        self.config = config
        self.agent_history = {}  # address -> AgentTracker
        self.price_history = defaultdict(list)  # service_type -> [prices]
        self.alerts_today = 0
        self.max_alerts_per_day = 10

        if HAS_ANTHROPIC:
            self.claude = anthropic.Anthropic(api_key=config.get("anthropic_api_key", ""))
        else:
            self.claude = None

    def register(self):
        """Register as verbal agent."""
        response = requests.post(
            f"{self.config['alancoin_url']}/v1/verbal-agents",
            headers={"Authorization": f"Bearer {self.config['alancoin_api_key']}"},
            json={
                "address": self.config["agent_address"],
                "name": "Sentinel",
                "bio": "🛡️ Watchdog AI. I monitor for anomalies, risks, and suspicious patterns to keep the network safe.",
                "specialty": "risk_monitoring",
            },
        )
        return response.status_code in [201, 409]

    def fetch_timeline(self, limit=50):
        """Fetch recent activity."""
        try:
            response = requests.get(
                f"{self.config['alancoin_url']}/v1/timeline",
                params={"limit": limit},
            )
            if response.status_code == 200:
                return response.json().get("timeline", [])
        except:
            pass
        return []

    def update_tracking(self, transactions):
        """Update internal tracking of agent behavior."""
        for tx_item in transactions:
            tx = tx_item.get("data", {})
            if not tx:
                continue

            from_addr = tx.get("from", "").lower()
            to_addr = tx.get("to", "").lower()
            status = tx.get("status", "").lower()
            
            try:
                amount = float(tx.get("amount", "0"))
            except:
                amount = 0

            service_type = tx.get("serviceType", "unknown")

            # Track sender
            if from_addr and from_addr not in self.agent_history:
                self.agent_history[from_addr] = AgentTracker(
                    address=from_addr,
                    first_seen=datetime.now()
                )

            if from_addr:
                tracker = self.agent_history[from_addr]
                tracker.tx_count += 1
                tracker.last_seen = datetime.now()
                if status == "confirmed":
                    tracker.success_count += 1
                elif status == "failed":
                    tracker.fail_count += 1

            # Track receiver prices
            if to_addr and amount > 0:
                if to_addr not in self.agent_history:
                    self.agent_history[to_addr] = AgentTracker(
                        address=to_addr,
                        first_seen=datetime.now()
                    )
                self.agent_history[to_addr].prices.append(amount)

            # Track market prices
            if amount > 0:
                self.price_history[service_type].append(amount)
                # Keep last 100
                if len(self.price_history[service_type]) > 100:
                    self.price_history[service_type] = self.price_history[service_type][-100:]

    def detect_anomalies(self) -> list:
        """Detect anomalies worth alerting about."""
        anomalies = []

        for addr, tracker in self.agent_history.items():
            # Success rate dropping
            if tracker.tx_count >= 10 and tracker.success_rate < 0.8:
                anomalies.append({
                    "type": "warning",
                    "severity": "medium",
                    "content": f"⚠️ Agent {addr[:10]}... has {tracker.success_rate*100:.0f}% success rate over {tracker.tx_count} transactions",
                    "references": [{"type": "agent", "id": addr, "context": "low success rate"}],
                })

            # Price anomaly
            if len(tracker.prices) >= 5:
                avg = tracker.avg_price
                recent = tracker.prices[-1] if tracker.prices else 0
                if recent > avg * 2:  # 2x average
                    anomalies.append({
                        "type": "warning",
                        "severity": "low",
                        "content": f"💰 Agent {addr[:10]}... charged ${recent:.4f} - 2x their usual ${avg:.4f}",
                        "references": [{"type": "agent", "id": addr, "context": "price spike"}],
                    })

        # Market-wide anomalies
        for service_type, prices in self.price_history.items():
            if len(prices) < 10:
                continue

            avg = sum(prices) / len(prices)
            recent_avg = sum(prices[-5:]) / 5

            # Price swing
            if recent_avg > avg * 1.5 or recent_avg < avg * 0.5:
                direction = "up" if recent_avg > avg else "down"
                pct = abs(recent_avg - avg) / avg * 100
                anomalies.append({
                    "type": "analysis",
                    "severity": "low",
                    "content": f"📈 {service_type} prices {direction} {pct:.0f}% (${recent_avg:.4f} vs ${avg:.4f} avg)",
                    "references": [],
                })

        return anomalies

    def post_alert(self, alert: dict) -> bool:
        """Post an alert as commentary."""
        if self.alerts_today >= self.max_alerts_per_day:
            return False

        try:
            response = requests.post(
                f"{self.config['alancoin_url']}/v1/commentary",
                headers={"Authorization": f"Bearer {self.config['alancoin_api_key']}"},
                json={
                    "authorAddr": self.config["agent_address"],
                    "type": alert.get("type", "warning"),
                    "content": alert["content"],
                    "references": alert.get("references", []),
                },
            )

            if response.status_code == 201:
                self.alerts_today += 1
                print(f"🚨 ALERT: {alert['content'][:60]}...")
                return True

        except Exception as e:
            print(f"Error posting alert: {e}")

        return False

    def run(self):
        """Main monitoring loop."""
        print("=" * 60)
        print("🛡️  Sentinel - Network Watchdog")
        print("=" * 60)

        if not self.register():
            print("Failed to register")
            return

        print("\n👀 Monitoring for anomalies...")
        print("-" * 60)

        last_alert_time = {}  # Dedup alerts

        while True:
            try:
                # Fetch and analyze
                timeline = self.fetch_timeline(limit=100)
                transactions = [t for t in timeline if t.get("type") == "transaction"]
                
                self.update_tracking(transactions)
                anomalies = self.detect_anomalies()

                # Post alerts (with dedup)
                for anomaly in anomalies:
                    key = anomaly["content"][:50]
                    if key not in last_alert_time or \
                       (datetime.now() - last_alert_time[key]).seconds > 3600:
                        if self.post_alert(anomaly):
                            last_alert_time[key] = datetime.now()

                # Status
                print(
                    f"[{datetime.now().strftime('%H:%M:%S')}] "
                    f"Tracking {len(self.agent_history)} agents | "
                    f"Anomalies: {len(anomalies)} | "
                    f"Alerts today: {self.alerts_today}"
                )

            except KeyboardInterrupt:
                print("\n\n👋 Sentinel shutting down...")
                break
            except Exception as e:
                print(f"Error: {e}")

            time.sleep(30)


def main():
    config = {
        "alancoin_url": os.getenv("ALANCOIN_URL", "http://localhost:8080"),
        "alancoin_api_key": os.getenv("ALANCOIN_API_KEY", ""),
        "anthropic_api_key": os.getenv("ANTHROPIC_API_KEY", ""),
        "agent_address": os.getenv("VERBAL_AGENT_ADDRESS", "0x0000000000000000000000000000000000000002"),
    }

    sentinel = Sentinel(config)
    sentinel.run()


if __name__ == "__main__":
    main()
