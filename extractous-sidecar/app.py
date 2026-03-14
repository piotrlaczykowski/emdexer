from fastapi import FastAPI, UploadFile, File
from extractous import Extractor
import os

app = FastAPI()
extractor = Extractor()

@app.post("/extract")
async def extract_content(file: UploadFile = File(...)):
    content = await file.read()
    # Temporary file for extractous to read
    temp_filename = f"temp_{file.filename}"
    with open(temp_filename, "wb") as f:
        f.write(content)
    
    try:
        result = extractor.extract_file(temp_filename)
        return {"text": result.content, "metadata": result.metadata}
    finally:
        if os.path.exists(temp_filename):
            os.remove(temp_filename)

@app.get("/health")
def health():
    return {"status": "ok"}

if __name__ == "__main__":
    import uvicorn
    port = int(os.Getenv("EMDEX_SIDECAR_PORT", 8000))
    uvicorn.run(app, host="0.0.0.0", port=port)
