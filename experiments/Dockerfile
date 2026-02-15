FROM python:3.12-slim

WORKDIR /app

# Install system deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install Python deps
COPY pyproject.toml .
RUN pip install --no-cache-dir -e ".[all]"

# Copy harness code
COPY . .

# Default: run smoke test
CMD ["python", "smoke_test.py"]
