#!/bin/bash
# Deploy Alancoin to Fly.io
#
# Prerequisites:
# 1. Install flyctl: curl -L https://fly.io/install.sh | sh
# 2. Login: fly auth login
# 3. Have a funded Base Sepolia wallet
#
# Usage: ./scripts/deploy.sh

set -e

echo "=========================================="
echo "Alancoin Deployment"
echo "=========================================="

# Check if flyctl is installed
if ! command -v fly &> /dev/null; then
    echo "Error: flyctl not installed"
    echo "Install: curl -L https://fly.io/install.sh | sh"
    exit 1
fi

# Check if logged in
if ! fly auth whoami &> /dev/null; then
    echo "Error: Not logged in to Fly.io"
    echo "Run: fly auth login"
    exit 1
fi

echo ""
echo "Logged in as: $(fly auth whoami)"
echo ""

# Check if app exists
if fly apps list | grep -q "alancoin"; then
    echo "App 'alancoin' exists. Deploying update..."
    fly deploy
else
    echo "Creating new app..."
    
    # Launch creates the app
    fly launch --no-deploy --copy-config --name alancoin
    
    echo ""
    echo "=========================================="
    echo "IMPORTANT: Set your private key"
    echo "=========================================="
    echo ""
    echo "Run this command with your wallet's private key:"
    echo ""
    echo "  fly secrets set PRIVATE_KEY=your_64_char_hex_key"
    echo ""
    echo "Get testnet funds:"
    echo "  - ETH: https://www.alchemy.com/faucets/base-sepolia"
    echo "  - USDC: https://faucet.circle.com (select Base Sepolia)"
    echo ""
    echo "Then deploy:"
    echo ""
    echo "  fly deploy"
    echo ""
    exit 0
fi

echo ""
echo "=========================================="
echo "Deployment complete!"
echo "=========================================="
echo ""
echo "App URL: https://alancoin.fly.dev"
echo ""
echo "Test it:"
echo "  curl https://alancoin.fly.dev/health"
echo "  curl https://alancoin.fly.dev/v1/network/stats"
echo ""
echo "View logs:"
echo "  fly logs"
echo ""
