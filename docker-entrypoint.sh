#!/bin/sh
set -e

# Run migrations if DATABASE_URL is set
if [ -n "$DATABASE_URL" ]; then
    echo "Running database migrations..."
    /app/migrate up
    echo "Migrations complete."
fi

# Start server
exec /app/alancoin "$@"
