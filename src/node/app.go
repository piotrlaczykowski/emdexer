package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extract"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/health"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/nodereg"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/safenet"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var EmbeddingDims uint64 = DefaultEmbedDims

var globalPointsClient qdrant.PointsClient
var globalQueue *queue.PersistentQueue
var globalCB *extractor.CircuitBreaker
var globalCfg Config
var globalCtx context.Context
var globalFS vfs.FileSystem
var globalEmbedder embed.EmbedProvider
var globalWorkerHeartbeat *watcher.Heartbeat

// walkSeen prevents duplicate indexing when the initial Walk and the real-time
// watcher overlap during startup. The Walk goroutine records every path it
// processes; the watcher callback skips paths already seen until walkComplete
// is set to 1 (meaning the startup walk finished).
var walkSeen sync.Map       // map[string]struct{}
var walkComplete atomic.Int32 // 0 = walk in progress, 1 = walk done

// App holds the wired-up application state returned by newApp.
type App struct {
	conn *grpc.ClientConn
	root string
	cwd  string
}

// newApp reads environment variables, wires up all dependencies, and starts
// background goroutines. It does NOT block; call Run() to block until signal.
func newApp() *App {
	cwd, _ := os.Getwd()
	config.LoadEnv(filepath.Join(cwd, ".env"))

	if dimStr := os.Getenv("EMDEX_EMBEDDING_DIMS"); dimStr != "" {
		if d, err := strconv.ParseUint(dimStr, 10, 64); err == nil && d > 0 {
			EmbeddingDims = d
		} else {
			log.Fatalf("invalid EMDEX_EMBEDDING_DIMS=%q: must be a positive integer", dimStr)
		}
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		log.Printf("WARNING: GOOGLE_API_KEY is not set — embedding and LLM features will be unavailable")
	}
	qdrantHost := os.Getenv("QDRANT_HOST")
	if qdrantHost == "" {
		qdrantHost = "localhost:6334"
	}

	nodeID := os.Getenv("EMDEX_NODE_ID")
	if nodeID == "" {
		hostname, _ := os.Hostname()
		nodeID = fmt.Sprintf("node-%s-%d", hostname, os.Getpid())
	}

	globalCfg = Config{
		QdrantHost:     qdrantHost,
		ExtractousHost: os.Getenv("EMDEX_EXTRACTOUS_URL"),
		CollectionName: CollectionName,
		GoogleAPIKey:   apiKey,
		Namespace:      os.Getenv("EMDEX_NAMESPACE"),
		NodeType:       os.Getenv("NODE_TYPE"),
		GatewayURL:     os.Getenv("EMDEX_GATEWAY_URL"),
		GatewayAuthKey: os.Getenv("EMDEX_GATEWAY_AUTH_KEY"),
		NodeID:         nodeID,
		SMBHost:        os.Getenv("SMB_HOST"),
		SMBUser:        os.Getenv("SMB_USER"),
		SMBPass:        os.Getenv("SMB_PASS"),
		SMBShare:       os.Getenv("SMB_SHARE"),
		SFTPHost:       os.Getenv("SFTP_HOST"),
		SFTPPort:       os.Getenv("SFTP_PORT"),
		SFTPUser:       os.Getenv("SFTP_USER"),
		SFTPPass:       os.Getenv("SFTP_PASS"),
		NFSHost:        os.Getenv("NFS_HOST"),
		NFSPath:        os.Getenv("NFS_PATH"),
		S3Endpoint:     os.Getenv("S3_ENDPOINT"),
		S3AccessKey:    os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:    os.Getenv("S3_SECRET_KEY"),
		S3Bucket:       os.Getenv("S3_BUCKET"),
		S3UseSSL:       os.Getenv("S3_USE_SSL") == "true",
		S3Prefix:       os.Getenv("S3_PREFIX"),
		WhisperURL:       os.Getenv("EMDEX_WHISPER_URL"),
		WhisperModel:     os.Getenv("EMDEX_WHISPER_MODEL"),
		WhisperEnabled:   os.Getenv("EMDEX_WHISPER_ENABLED") == "true",
		WhisperMinChars:  parseIntEnv("EMDEX_WHISPER_MIN_CHARS", 50),
		WhisperLanguage:  os.Getenv("EMDEX_WHISPER_LANGUAGE"),
		EnableOCR:        os.Getenv("EMDEX_ENABLE_OCR") == "true",
		VisionEnabled:    os.Getenv("EMDEX_VISION_ENABLED") == "true",
		VisionMaxSizeMB:  parseIntEnv("EMDEX_VISION_MAX_SIZE_MB", 10),
		FrameEnabled:     os.Getenv("EMDEX_FRAME_ENABLED") == "true",
		FFmpegURL:        os.Getenv("EMDEX_FFMPEG_URL"),
		FrameIntervalSec: parseIntEnv("EMDEX_FRAME_INTERVAL_SEC", 30),
		MaxFrames:        parseIntEnv("EMDEX_MAX_FRAMES", 10),
		ChunkSize:           parseIntEnv("EMDEX_CHUNK_SIZE", 512),
		ChunkOverlap:        parseIntEnv("EMDEX_CHUNK_OVERLAP", 50),
		ContextualRetrieval: os.Getenv("EMDEX_CONTEXTUAL_RETRIEVAL") == "true",
		ContextModel:        contextModel(),
	}

	chunkStrategy := os.Getenv("EMDEX_CHUNK_STRATEGY")
	semanticThreshold := parseFloatEnv("EMDEX_SEMANTIC_CHUNK_THRESHOLD", 0.7)
	semanticMaxSize := parseIntEnv("EMDEX_SEMANTIC_CHUNK_MAX_SIZE", 512)

	if globalCfg.ExtractousHost == "" {
		globalCfg.ExtractousHost = "http://localhost:8000/extract"
	}
	if globalCfg.NodeType == "" {
		globalCfg.NodeType = "local"
	}
	if globalCfg.Namespace == "" {
		// For S3 nodes, default the namespace to the bucket name so it registers
		// as a distinct data source in the cluster topology.
		if globalCfg.NodeType == "s3" && globalCfg.S3Bucket != "" {
			globalCfg.Namespace = "s3/" + globalCfg.S3Bucket
			log.Printf("📦 [node] S3 namespace auto-set to %q from bucket name", globalCfg.Namespace)
		} else {
			globalCfg.Namespace = "default"
		}
	}

	globalCB = extractor.NewCircuitBreaker(5, 5*time.Minute)
	globalCtx = context.Background()
	qdrantDialOpt, err := qdrantcreds.FromEnv()
	if err != nil {
		log.Fatalf("qdrant TLS config: %v", err)
	}
	conn, err := grpc.NewClient(globalCfg.QdrantHost, qdrantDialOpt,
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(32*1024*1024),
		),
	)
	if err != nil {
		panic(err)
	}

	globalEmbedder = embed.New(
		globalCfg.GoogleAPIKey,
		os.Getenv("EMBED_PROVIDER"),
		os.Getenv("OLLAMA_HOST"),
		os.Getenv("OLLAMA_EMBED_MODEL"),
		os.Getenv("EMDEX_GEMINI_MODEL"),
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_EMBED_MODEL"),
	)

	collectionsClient := qdrant.NewCollectionsClient(conn)
	globalPointsClient = qdrant.NewPointsClient(conn)

	root := os.Getenv("NODE_ROOT")
	if root == "" {
		root = filepath.Join(cwd, "test_dir")
	}
	initVFS(root)

	// Create extract client and pipeline config after VFS, CB, and embedder are ready.
	safeHTTP := safenet.NewSafeClient(60 * time.Second)
	extractClient := &extract.Client{
		CB:              globalCB,
		FS:              globalFS,
		HTTP:            safeHTTP,
		EnableOCR:       globalCfg.EnableOCR,
		VisionEnabled:   globalCfg.VisionEnabled,
		VisionMaxSizeMB: globalCfg.VisionMaxSizeMB,
		VisionAPIKey:    globalCfg.GoogleAPIKey,
	}

	// Configure Whisper sidecar if URL is set.
	if globalCfg.WhisperURL != "" {
		whisperCB := extractor.NewCircuitBreaker(5, 5*time.Minute)
		extractClient.Whisper = &extract.WhisperClient{
			URL:      globalCfg.WhisperURL,
			Model:    globalCfg.WhisperModel,
			HTTP:     safeHTTP,
			CB:       whisperCB,
			Enabled:  globalCfg.WhisperEnabled,
			MinChars: globalCfg.WhisperMinChars,
			Language: globalCfg.WhisperLanguage,
		}
		log.Printf("[node] Whisper sidecar configured: %s (model=%s enabled=%v)",
			globalCfg.WhisperURL, globalCfg.WhisperModel, globalCfg.WhisperEnabled)
	}

	// Configure FFmpeg sidecar if frame extraction is enabled.
	if globalCfg.FrameEnabled && globalCfg.FFmpegURL != "" {
		extractClient.Frames = &extract.FFmpegClient{
			URL:         globalCfg.FFmpegURL,
			HTTP:        safeHTTP,
			IntervalSec: globalCfg.FrameIntervalSec,
			MaxFrames:   globalCfg.MaxFrames,
		}
		log.Printf("[node] FFmpeg sidecar configured: %s (interval=%ds max_frames=%d)",
			globalCfg.FFmpegURL, globalCfg.FrameIntervalSec, globalCfg.MaxFrames)
	}

	if globalCfg.EnableOCR {
		log.Println("[node] OCR enabled for images and scanned PDFs")
	}
	if globalCfg.VisionEnabled {
		log.Println("[node] Gemini Vision enabled for image captioning")
	}

	var contextLLM func(string) (string, error)
	if globalCfg.ContextualRetrieval {
		apiKey := globalCfg.GoogleAPIKey
		model := globalCfg.ContextModel
		contextLLM = func(prompt string) (string, error) {
			return callGeminiGenerate(prompt, apiKey, model)
		}
	}

	var chunker indexer.ChunkStrategy
	if strings.ToLower(chunkStrategy) == "semantic" {
		embedderForChunking := globalEmbedder
		chunker = indexer.SemanticChunker{
			MaxChunkWords: semanticMaxSize,
			Threshold:     float32(semanticThreshold),
			Embedder: func(text string) ([]float32, error) {
				return embedderForChunking.Embed(context.Background(), text)
			},
		}
		log.Printf("[node] chunk strategy: semantic (threshold=%.2f max_words=%d)",
			semanticThreshold, semanticMaxSize)
	} else {
		log.Printf("[node] chunk strategy: fixed (size=%d overlap=%d)",
			globalCfg.ChunkSize, globalCfg.ChunkOverlap)
	}

	pipelineCfg := indexer.PipelineConfig{
		Namespace:           globalCfg.Namespace,
		ExtractousHost:      globalCfg.ExtractousHost,
		NodeType:            globalCfg.NodeType,
		Embedder:            globalEmbedder,
		ChunkSize:           globalCfg.ChunkSize,
		ChunkOverlap:        globalCfg.ChunkOverlap,
		Chunker:             chunker,
		ContextualRetrieval: globalCfg.ContextualRetrieval,
		ContextLLM:          contextLLM,
		Extract: func(path string, content []byte, host string) (string, map[string]string, error) {
			if len(content) > 0 {
				return extractClient.ExtractFromBytes(path, content, host)
			}
			return extractClient.ExtractContent(path, host)
		},
	}

	_, err = collectionsClient.Get(globalCtx, &qdrant.GetCollectionInfoRequest{
		CollectionName: globalCfg.CollectionName,
	})

	if err != nil {
		_, _ = collectionsClient.Create(globalCtx, &qdrant.CreateCollection{
			CollectionName: globalCfg.CollectionName,
			VectorsConfig: &qdrant.VectorsConfig{
				Config: &qdrant.VectorsConfig_Params{
					Params: &qdrant.VectorParams{
						Size:     EmbeddingDims,
						Distance: qdrant.Distance_Cosine,
					},
				},
			},
		})
	}

	// Ensure full-text payload indexes exist for hybrid (BM25 + vector) search.
	registry.EnsureTextIndexes(globalCtx, globalPointsClient, globalCfg.CollectionName)

	// Hoist cacheDir so it is available for both migration check and poller setup.
	cacheDir := os.Getenv("EMDEX_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(cwd, "cache")
	}
	_ = os.MkdirAll(cacheDir, 0700)

	// Automatic graph-relation migration: if the collection predates Phase 24
	// (i.e. <20% of sampled chunk-0 points carry a `relations` field), delete
	// the metadata cache so the next poller/walk treats every file as new and
	// re-indexes it with relation extraction enabled.
	migrationMode := parseGraphMigrationMode(os.Getenv("EMDEX_GRAPH_MIGRATION"))
	checkRelationsMigration(globalCtx, globalPointsClient, globalCfg.CollectionName,
		globalCfg.Namespace, cacheDir, globalCfg.NodeType, migrationMode)

	indexWorkers := 1
	if v := os.Getenv("EMDEX_INDEX_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 16 {
			indexWorkers = n
		}
	}
	log.Printf("[node] indexing workers: %d", indexWorkers)
	startIndexing(root, cwd, pipelineCfg, indexWorkers)

	// Self-register with the gateway and start periodic heartbeat.
	nodeCfg := nodereg.NodeConfig{
		GatewayURL:     globalCfg.GatewayURL,
		GatewayAuthKey: globalCfg.GatewayAuthKey,
		NodeID:         globalCfg.NodeID,
		CollectionName: globalCfg.CollectionName,
		Namespace:      globalCfg.Namespace,
		NodeType:       globalCfg.NodeType,
	}
	nodereg.Register(nodeCfg)
	go nodereg.StartHeartbeatLoop(nodeCfg)

	return &App{conn: conn, root: root, cwd: cwd}
}

// Run starts the health server and blocks until SIGTERM or SIGINT.
func (a *App) Run() {
	defer func() { _ = a.conn.Close() }()
	defer func() { _ = globalFS.Close() }()

	go health.StartServer(health.ServerConfig{
		QdrantConn:      a.conn,
		WorkerHeartbeat: globalWorkerHeartbeat,
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Printf("[node] Shutting down")
}
