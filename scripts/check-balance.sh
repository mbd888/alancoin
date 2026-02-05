#!/bin/bash
# Check USDC balance on Base Sepolia

set -e

# Load .env if exists
if [ -f .env ]; then
    export $(grep -v '^#' .env | xargs)
fi

if [ -z "$WALLET_ADDRESS" ]; then
    echo "Error: WALLET_ADDRESS not set"
    exit 1
fi

RPC_URL="${RPC_URL:-https://sepolia.base.org}"
USDC_CONTRACT="${USDC_CONTRACT:-0x036CbD53842c5426634e7929541eC2318f3dCF7e}"

echo "Checking balance for: $WALLET_ADDRESS"
echo "Chain: Base Sepolia"
echo "RPC: $RPC_URL"

# balanceOf(address) selector = 0x70a08231
# Pad address to 32 bytes
PADDED_ADDRESS=$(printf '%064s' "${WALLET_ADDRESS:2}" | tr ' ' '0')
DATA="0x70a08231${PADDED_ADDRESS}"

# Make the call
RESULT=$(curl -s -X POST "$RPC_URL" \
    -H "Content-Type: application/json" \
    -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$USDC_CONTRACT\",\"data\":\"$DATA\"},\"latest\"],\"id\":1}")

# Extract result
HEX_BALANCE=$(echo "$RESULT" | grep -o '"result":"[^"]*"' | cut -d'"' -f4)

if [ -z "$HEX_BALANCE" ] || [ "$HEX_BALANCE" = "0x" ]; then
    echo "Balance: 0 USDC"
    exit 0
fi

# Convert hex to decimal (remove 0x prefix)
BALANCE_RAW=$(printf '%d' "$HEX_BALANCE" 2>/dev/null || echo "0")

# USDC has 6 decimals
BALANCE=$(echo "scale=6; $BALANCE_RAW / 1000000" | bc)

echo "Balance: $BALANCE USDC"
