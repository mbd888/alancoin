#!/usr/bin/env python3
"""
Stub Service Provider for Alancoin Demo

A simple HTTP server that acts as a real AI service provider.
The gateway proxy forwards requests here, and this server returns
fake but plausible responses for each service type.

Usage:
    python3 scripts/stub_provider.py [--port 9090]
"""

import argparse
import json
import random
import string
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler
from datetime import datetime


class StubProviderHandler(BaseHTTPRequestHandler):
    """Handles incoming requests forwarded by the Alancoin gateway."""

    def log_message(self, fmt, *args):
        """Override to add payment header logging."""
        timestamp = datetime.now().strftime("%H:%M:%S")
        sys.stderr.write(f"[{timestamp}] [stub] {fmt % args}\n")

    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length > 0 else b"{}"

        try:
            data = json.loads(body)
        except json.JSONDecodeError:
            data = {}

        # Log payment headers from the gateway
        payment_amount = self.headers.get("X-Payment-Amount", "")
        payment_from = self.headers.get("X-Payment-From", "")
        payment_ref = self.headers.get("X-Payment-Ref", "")
        if payment_amount:
            self.log_message(
                "Payment: %s USDC from %s (ref: %s)",
                payment_amount, payment_from[:12] + "..." if payment_from else "?", payment_ref[:16] + "..." if payment_ref else "?"
            )

        # Route to the appropriate handler
        path = self.path.rstrip("/")
        handlers = {
            "/inference": self.handle_inference,
            "/translation": self.handle_translation,
            "/code-review": self.handle_code_review,
            "/embedding": self.handle_embedding,
        }

        handler = handlers.get(path)
        if handler is None:
            self.send_error_response(404, f"Unknown service endpoint: {path}")
            return

        try:
            result = handler(data)
            self.send_json_response(200, result)
        except Exception as e:
            self.send_error_response(500, str(e))

    def handle_inference(self, data):
        """POST /inference - AI inference (summarize, classify, analyze)."""
        text = data.get("text", "No input provided")
        task = data.get("task", "summarize")

        results = {
            "summarize": {
                "output": f"Summary: {text[:80]}... Key points: (1) Primary topic identified, "
                          f"(2) {random.randint(3, 8)} entities extracted, "
                          f"(3) Sentiment: {'positive' if random.random() > 0.4 else 'neutral'}.",
                "model": "gpt-4-turbo",
                "tokens_used": random.randint(150, 500),
            },
            "classify": {
                "output": random.choice([
                    "technology", "finance", "healthcare", "education", "entertainment"
                ]),
                "confidence": round(random.uniform(0.82, 0.99), 3),
                "model": "classifier-v3",
                "tokens_used": random.randint(50, 150),
            },
            "analyze": {
                "output": f"Analysis complete. Found {random.randint(2, 6)} key themes. "
                          f"Readability score: {round(random.uniform(60, 95), 1)}. "
                          f"Complexity: {'high' if random.random() > 0.5 else 'medium'}.",
                "model": "analyst-v2",
                "tokens_used": random.randint(200, 800),
            },
        }

        result = results.get(task, results["summarize"])
        result["service"] = "inference"
        result["task"] = task
        return result

    def handle_translation(self, data):
        """POST /translation - Language translation."""
        text = data.get("text", "Hello, world!")
        target = data.get("target", "es")

        translations = {
            "es": {"prefix": "Traduccion: ", "lang": "Spanish"},
            "fr": {"prefix": "Traduction: ", "lang": "French"},
            "de": {"prefix": "Ubersetzung: ", "lang": "German"},
            "ja": {"prefix": "Honyaku: ", "lang": "Japanese"},
        }

        t = translations.get(target, translations["es"])
        return {
            "output": f"{t['prefix']}{text[:100]}",
            "source_language": "en",
            "target_language": target,
            "language_name": t["lang"],
            "service": "translation",
            "characters": len(text),
        }

    def handle_code_review(self, data):
        """POST /code-review - Automated code review."""
        code = data.get("code", "# no code provided")
        lines = code.count("\n") + 1

        issues = random.randint(0, 4)
        severity_choices = ["info", "warning", "error"]
        findings = []
        for i in range(issues):
            findings.append({
                "line": random.randint(1, max(1, lines)),
                "severity": random.choice(severity_choices),
                "message": random.choice([
                    "Consider extracting this into a helper function",
                    "Missing error handling for this operation",
                    "Variable name could be more descriptive",
                    "This loop could be simplified with a list comprehension",
                    "Add type hints for better maintainability",
                    "Potential null reference - add a guard clause",
                ]),
            })

        return {
            "output": f"Code review complete: {issues} issue(s) found across {lines} line(s).",
            "issues_found": issues,
            "findings": findings,
            "quality_score": round(random.uniform(6.5, 9.8), 1),
            "service": "code-review",
        }

    def handle_embedding(self, data):
        """POST /embedding - Text embedding vector generation."""
        text = data.get("text", "")
        dim = 8  # Small dimension for demo purposes

        # Generate a fake but consistent-looking embedding vector
        vector = [round(random.uniform(-1.0, 1.0), 6) for _ in range(dim)]

        return {
            "output": f"Embedding generated: {dim}-dimensional vector for {len(text)} chars of text.",
            "embedding": vector,
            "dimensions": dim,
            "model": "embed-v3",
            "service": "embedding",
        }

    def send_json_response(self, status, data):
        """Send a JSON response."""
        body = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def send_error_response(self, status, message):
        """Send a JSON error response."""
        self.send_json_response(status, {"error": message})


def main():
    parser = argparse.ArgumentParser(description="Alancoin Stub Service Provider")
    parser.add_argument("--port", type=int, default=9090, help="Port to listen on (default: 9090)")
    args = parser.parse_args()

    server = HTTPServer(("127.0.0.1", args.port), StubProviderHandler)
    timestamp = datetime.now().strftime("%H:%M:%S")
    print(f"[{timestamp}] [stub] Stub provider listening on http://127.0.0.1:{args.port}")
    print(f"[{timestamp}] [stub] Endpoints: /inference, /translation, /code-review, /embedding")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
