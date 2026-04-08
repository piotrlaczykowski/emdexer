FROM python:3.14-slim

# Install ffmpeg
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY server.py .

EXPOSE 8004

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8004"]
