package main

const (
	DefaultEmbedDims = 3072
	CollectionName   = "emdexer_v1"
)

type Config struct {
	QdrantHost     string
	ExtractousHost string
	CollectionName string
	GoogleAPIKey   string
	Namespace      string
	NodeType       string
	GatewayURL     string
	GatewayAuthKey string
	NodeID         string
	SMBHost        string
	SMBUser        string
	SMBPass        string
	SMBShare       string
	SFTPHost       string
	SFTPPort       string
	SFTPUser       string
	SFTPPass       string
	NFSHost        string
	NFSPath        string
	S3Endpoint     string
	S3AccessKey    string
	S3SecretKey    string
	S3Bucket       string
	S3UseSSL       bool
	S3Prefix       string
	ChunkSize    int    // EMDEX_CHUNK_SIZE — words per chunk; default 512
	ChunkOverlap int    // EMDEX_CHUNK_OVERLAP — overlapping words; default 50
	ContextualRetrieval bool   // EMDEX_CONTEXTUAL_RETRIEVAL — prepend LLM context to chunk embeddings
	ContextModel        string // EMDEX_CONTEXT_MODEL — model for context generation
	WhisperURL      string // Whisper sidecar URL (e.g. http://whisper:8080)
	WhisperModel    string // Whisper model name (default: "base")
	WhisperEnabled  bool   // EMDEX_WHISPER_ENABLED — master toggle for audio transcription
	WhisperMinChars int    // EMDEX_WHISPER_MIN_CHARS — minimum transcript length
	WhisperLanguage string // EMDEX_WHISPER_LANGUAGE — optional language hint
	EnableOCR       bool   // Enable OCR for images and scanned PDFs
	VisionEnabled   bool   // EMDEX_VISION_ENABLED — enable Gemini Vision image captioning
	VisionMaxSizeMB int    // EMDEX_VISION_MAX_SIZE_MB — skip images larger than this
	FrameEnabled    bool   // EMDEX_FRAME_ENABLED — enable FFmpeg video frame extraction
	FFmpegURL       string // EMDEX_FFMPEG_URL — FFmpeg sidecar URL
	FrameIntervalSec int   // EMDEX_FRAME_INTERVAL_SEC — seconds between extracted frames
	MaxFrames        int   // EMDEX_MAX_FRAMES — maximum frames per video
}
