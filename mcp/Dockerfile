FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y curl && rm -rf /var/lib/apt/lists/*

# Install fastmcp with prefab-ui support
RUN pip install "fastmcp[apps]" requests

COPY main.py .

# Use SSE by default for interactive apps, or stdio for Claude Desktop
# Here we use the standard fastmcp run which supports both depending on CMD
CMD ["python", "main.py", "run", "--port", "8002", "--transport", "sse"]
