FROM python:3.11-slim

RUN apt-get update && apt-get install -y     libmagic1     tesseract-ocr     libtesseract-dev     curl     && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir extractous fastapi uvicorn python-multipart

COPY app.py .

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000"]
