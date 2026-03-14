package extractor

import (
	"context"
	"fmt"
)

// VideoSampler extracts semantic content from video files.
// Full implementation requires ffmpeg (frame sampling) and Whisper (audio transcription).
type VideoSampler struct{}

// SampleFrames extracts visual frames from a video and returns a description.
// NOT YET IMPLEMENTED — requires ffmpeg and Gemini Vision API integration.
// Future: ffmpeg -i video.mp4 -vf "fps=1/10" frames_%03d.jpg → Gemini Vision
func (v *VideoSampler) SampleFrames(ctx context.Context, videoPath string) (string, error) {
	return "", fmt.Errorf("video frame extraction not yet implemented: requires ffmpeg and Gemini Vision API integration")
}

// ProcessWhisper transcribes audio content using a speech-to-text engine.
// NOT YET IMPLEMENTED — requires Whisper API or local whisper.cpp binary.
// Future: POST audio bytes to /v1/audio/transcriptions (OpenAI-compatible endpoint)
func (v *VideoSampler) ProcessWhisper(ctx context.Context, audioContent []byte) (string, error) {
	return "", fmt.Errorf("audio transcription not yet implemented: requires Whisper API or local whisper.cpp integration")
}
