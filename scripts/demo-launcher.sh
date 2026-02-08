#!/bin/bash
#
# Alancoin YC Demo Launcher
# One command to start the full demo experience
#
# Usage: ./scripts/demo-launcher.sh [--fast|--slow]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SERVER_PID=""
SPEED="${1:-normal}"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[demo]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[demo]${NC} $1"
}

error() {
    echo -e "${RED}[demo]${NC} $1"
}

cleanup() {
    log "Cleaning up..."
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
    fi
    pkill -f "alancoin" 2>/dev/null || true
}

trap cleanup EXIT

# Parse speed argument
case "$1" in
    --fast)
        SPEED="fast"
        ;;
    --slow)
        SPEED="slow"
        ;;
    *)
        SPEED="normal"
        ;;
esac

echo ""
echo -e "${CYAN}================================================${NC}"
echo -e "${CYAN}  ALANCOIN - YC Demo Launcher${NC}"
echo -e "${CYAN}  Economic Infrastructure for AI Agents${NC}"
echo -e "${CYAN}================================================${NC}"
echo ""

# Check if server is already running
if curl -s "http://localhost:8080/health/live" > /dev/null 2>&1; then
    warn "Server already running at localhost:8080"
    log "Starting demo against existing server..."
    cd "$PROJECT_DIR"
    python3 scripts/demo.py --speed "$SPEED"
    exit 0
fi

# Check for .env file
if [ ! -f "$PROJECT_DIR/.env" ]; then
    if [ -f "$PROJECT_DIR/.env.example" ]; then
        warn "No .env file found. Creating from .env.example..."
        cp "$PROJECT_DIR/.env.example" "$PROJECT_DIR/.env"
        warn "Please edit .env with your PRIVATE_KEY before running with real transactions"
    else
        error "No .env file found and no .env.example to copy from"
        exit 1
    fi
fi

# Build the server
log "Building Alancoin server..."
cd "$PROJECT_DIR"
make build --silent

# Start the server in background
log "Starting server..."
./bin/alancoin &
SERVER_PID=$!

# Wait for server to be ready
log "Waiting for server to be ready..."
for i in {1..30}; do
    if curl -s "http://localhost:8080/health/live" > /dev/null 2>&1; then
        log "Server is ready!"
        break
    fi
    if [ "$i" -eq 30 ]; then
        error "Server failed to start within 30 seconds"
        exit 1
    fi
    sleep 1
done

# Small delay for full initialization
sleep 1

# Open dashboard in browser (macOS)
if command -v open &> /dev/null; then
    log "Opening dashboard in browser..."
    open "http://localhost:8080/"
fi

# Run the demo
echo ""
log "Starting live demo (speed: $SPEED)..."
echo ""

python3 scripts/demo.py --speed "$SPEED"

# Keep server running after demo for exploration
echo ""
log "Demo complete!"
log "Dashboard: http://localhost:8080/"
log "API: http://localhost:8080/v1/network/stats"
log "Enhanced Stats: http://localhost:8080/v1/network/stats/enhanced"
echo ""
log "Press Ctrl+C to stop the server"

# Wait for interrupt
wait $SERVER_PID
