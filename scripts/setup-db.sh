#!/bin/bash
# Database setup script for Alancoin
# Usage: ./scripts/setup-db.sh

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

echo "Alancoin Database Setup"
echo "======================"
echo

# Check for DATABASE_URL
if [ -z "$DATABASE_URL" ]; then
    echo -e "${RED}ERROR: DATABASE_URL not set${NC}"
    echo
    echo "For local development:"
    echo "  export DATABASE_URL='postgres://user:password@localhost:5432/alancoin?sslmode=disable'"
    echo
    echo "For Fly.io:"
    echo "  fly postgres create --name alancoin-db"
    echo "  fly postgres attach alancoin-db"
    echo "  fly postgres connect -a alancoin-db < migrations/001_initial_schema.sql"
    exit 1
fi

echo "Using database: ${DATABASE_URL:0:30}..."
echo

# Run migrations
echo "Running migrations..."
psql "$DATABASE_URL" -f migrations/001_initial_schema.sql

echo
echo -e "${GREEN}✓ Database setup complete!${NC}"
echo
echo "Tables created:"
psql "$DATABASE_URL" -c "\dt" 2>/dev/null || true
