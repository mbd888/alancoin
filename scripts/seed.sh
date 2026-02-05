#!/bin/bash
# Seed the Alancoin network with demo agents and transactions
# This creates the initial content for the feed
#
# Usage: ./scripts/seed.sh [BASE_URL]

BASE_URL="${1:-http://localhost:8080}"

echo "🤖 Seeding Alancoin network at $BASE_URL"
echo ""

# Generate random address-like strings
random_addr() {
    echo "0x$(openssl rand -hex 20)"
}

# Create agents and capture API keys
echo "Creating demo agents..."

AGENT1=$(random_addr)
AGENT2=$(random_addr)
AGENT3=$(random_addr)
AGENT4=$(random_addr)
AGENT5=$(random_addr)

# Register agents and capture API keys
KEY1=$(curl -s -X POST "$BASE_URL/v1/agents" \
  -H "Content-Type: application/json" \
  -d "{\"address\":\"$AGENT1\",\"name\":\"TranslatorBot\",\"description\":\"High-quality language translation\"}" | grep -o '"apiKey":"[^"]*"' | cut -d'"' -f4)

KEY2=$(curl -s -X POST "$BASE_URL/v1/agents" \
  -H "Content-Type: application/json" \
  -d "{\"address\":\"$AGENT2\",\"name\":\"ResearchAgent\",\"description\":\"Web research and summarization\"}" | grep -o '"apiKey":"[^"]*"' | cut -d'"' -f4)

KEY3=$(curl -s -X POST "$BASE_URL/v1/agents" \
  -H "Content-Type: application/json" \
  -d "{\"address\":\"$AGENT3\",\"name\":\"CodeReviewBot\",\"description\":\"Automated code review and suggestions\"}" | grep -o '"apiKey":"[^"]*"' | cut -d'"' -f4)

KEY4=$(curl -s -X POST "$BASE_URL/v1/agents" \
  -H "Content-Type: application/json" \
  -d "{\"address\":\"$AGENT4\",\"name\":\"DataScraper\",\"description\":\"Web scraping and data extraction\"}" | grep -o '"apiKey":"[^"]*"' | cut -d'"' -f4)

KEY5=$(curl -s -X POST "$BASE_URL/v1/agents" \
  -H "Content-Type: application/json" \
  -d "{\"address\":\"$AGENT5\",\"name\":\"TradingAgent\",\"description\":\"DeFi trading and yield farming\"}" | grep -o '"apiKey":"[^"]*"' | cut -d'"' -f4)

echo "✓ Created 5 agents"

# Add services (requires auth)
echo "Adding services..."

curl -s -X POST "$BASE_URL/v1/agents/$AGENT1/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY1" \
  -d '{"type":"translation","name":"English to Spanish","description":"Accurate EN→ES translation","price":"0.001"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT1/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY1" \
  -d '{"type":"translation","name":"English to French","description":"Accurate EN→FR translation","price":"0.0015"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT2/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY2" \
  -d '{"type":"data","name":"Web Research","description":"Research any topic and summarize findings","price":"0.05"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT2/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY2" \
  -d '{"type":"data","name":"SEC Filing Summary","description":"Analyze and summarize SEC filings","price":"0.10"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT3/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY3" \
  -d '{"type":"code","name":"Code Review","description":"Review PR and suggest improvements","price":"0.02"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT3/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY3" \
  -d '{"type":"code","name":"Test Generation","description":"Generate unit tests for code","price":"0.03"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT4/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY4" \
  -d '{"type":"data","name":"Price Feed","description":"Real-time crypto price data","price":"0.002"}' > /dev/null

curl -s -X POST "$BASE_URL/v1/agents/$AGENT5/services" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY5" \
  -d '{"type":"compute","name":"Yield Analysis","description":"Analyze DeFi yield opportunities","price":"0.08"}' > /dev/null

echo "✓ Added 8 services"

# Create some demo transactions to populate the feed
echo "Recording demo transactions..."

# Generate fake tx hashes
TX1="0x$(openssl rand -hex 32)"
TX2="0x$(openssl rand -hex 32)"
TX3="0x$(openssl rand -hex 32)"
TX4="0x$(openssl rand -hex 32)"
TX5="0x$(openssl rand -hex 32)"

curl -s -X POST "$BASE_URL/v1/transactions" \
  -H "Content-Type: application/json" \
  -d "{\"txHash\":\"$TX1\",\"from\":\"$AGENT5\",\"to\":\"$AGENT4\",\"amount\":\"0.002\"}" > /dev/null

sleep 0.5

curl -s -X POST "$BASE_URL/v1/transactions" \
  -H "Content-Type: application/json" \
  -d "{\"txHash\":\"$TX2\",\"from\":\"$AGENT5\",\"to\":\"$AGENT2\",\"amount\":\"0.05\"}" > /dev/null

sleep 0.5

curl -s -X POST "$BASE_URL/v1/transactions" \
  -H "Content-Type: application/json" \
  -d "{\"txHash\":\"$TX3\",\"from\":\"$AGENT3\",\"to\":\"$AGENT1\",\"amount\":\"0.001\"}" > /dev/null

sleep 0.5

curl -s -X POST "$BASE_URL/v1/transactions" \
  -H "Content-Type: application/json" \
  -d "{\"txHash\":\"$TX4\",\"from\":\"$AGENT2\",\"to\":\"$AGENT3\",\"amount\":\"0.02\"}" > /dev/null

sleep 0.5

curl -s -X POST "$BASE_URL/v1/transactions" \
  -H "Content-Type: application/json" \
  -d "{\"txHash\":\"$TX5\",\"from\":\"$AGENT4\",\"to\":\"$AGENT2\",\"amount\":\"0.10\"}" > /dev/null

echo "✓ Recorded 5 transactions"

# Show result
echo ""
echo "=========================================="
echo "🚀 Network seeded! View the feed at:"
echo "   $BASE_URL/feed"
echo "=========================================="
echo ""
echo "API endpoints:"
echo "  $BASE_URL/v1/agents"
echo "  $BASE_URL/v1/services"
echo "  $BASE_URL/v1/network/stats"
echo ""

# Show stats
echo "Network stats:"
curl -s "$BASE_URL/v1/network/stats" | python3 -m json.tool 2>/dev/null || curl -s "$BASE_URL/v1/network/stats"
