"""
FFmpeg sidecar — video frame extraction service.

Endpoints:
  GET  /health
  POST /frames?interval=30&max_frames=10
       Accepts a multipart video upload; returns base64-encoded JPEG frames.
"""

from __future__ import annotations

import base64
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Annotated

from fastapi import FastAPI, File, Query, UploadFile
from fastapi.responses import JSONResponse

app = FastAPI(title="emdexer-ffmpeg-sidecar", version="1.0.0")


@app.get("/health")
async def health() -> dict:
    return {"status": "ok"}


@app.post("/frames")
async def extract_frames(
    file: Annotated[UploadFile, File(description="Video file")],
    interval: Annotated[int, Query(ge=1, le=3600, description="Seconds between frames")] = 30,
    max_frames: Annotated[int, Query(ge=1, le=100, alias="max_frames", description="Maximum frames")] = 10,
) -> JSONResponse:
    """Extract JPEG frames from a video at the given interval, capped at max_frames."""

    data = await file.read()
    suffix = Path(file.filename or "video.mp4").suffix or ".mp4"

    with tempfile.TemporaryDirectory() as tmpdir:
        input_path = os.path.join(tmpdir, f"input{suffix}")
        frames_dir = os.path.join(tmpdir, "frames")
        os.makedirs(frames_dir, exist_ok=True)

        with open(input_path, "wb") as f:
            f.write(data)

        # Extract one frame every `interval` seconds, naming them by timestamp.
        cmd = [
            "ffmpeg",
            "-i", input_path,
            "-vf", f"fps=1/{interval}",
            "-frames:v", str(max_frames),
            "-q:v", "2",
            os.path.join(frames_dir, "frame_%05d.jpg"),
            "-y",
            "-loglevel", "error",
        ]

        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode != 0:
            return JSONResponse(
                status_code=500,
                content={"error": f"ffmpeg failed: {result.stderr.strip()}"},
            )

        frame_files = sorted(Path(frames_dir).glob("frame_*.jpg"))

        frames = []
        for idx, frame_path in enumerate(frame_files[:max_frames]):
            timestamp_sec = idx * interval
            with open(frame_path, "rb") as fh:
                jpeg_b64 = base64.b64encode(fh.read()).decode()
            frames.append({"timestamp_sec": timestamp_sec, "data": jpeg_b64})

    return JSONResponse(content={"frames": frames})
