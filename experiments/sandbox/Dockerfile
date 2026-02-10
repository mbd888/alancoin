FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

EXPOSE 8090

# Run from experiments dir so "sandbox" and "harness" are importable as top-level packages
CMD ["python", "-m", "sandbox"]
