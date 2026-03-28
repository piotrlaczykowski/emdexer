package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extract"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/health"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/nodereg"
	"github.com/piotrlaczykowski/emdexer/plugin"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/safenet"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
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

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		version.Print()
		return
	}
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
	conn, err := grpc.NewClient(globalCfg.QdrantHost, qdrantDialOpt)
	if err != nil {
		panic(err)
	}
	defer func() { _ = conn.Close() }()

	globalEmbedder = embed.New(
		globalCfg.GoogleAPIKey,
		os.Getenv("EMBED_PROVIDER"),
		os.Getenv("OLLAMA_HOST"),
		os.Getenv("OLLAMA_EMBED_MODEL"),
		os.Getenv("EMDEX_GEMINI_MODEL"),
	)

	collectionsClient := qdrant.NewCollectionsClient(conn)
	globalPointsClient = qdrant.NewPointsClient(conn)

	queuePath := os.Getenv("EMDEX_QUEUE_DB")
	if queuePath == "" {
		queuePath = filepath.Join(cwd, "cache", "queue.db")
	}
	_ = os.MkdirAll(filepath.Dir(queuePath), 0700)
	globalQueue, err = queue.NewPersistentQueue(queuePath)
	if err == nil {
		go queue.StartWorker(globalQueue, globalPointsClient, globalCfg.CollectionName, globalCtx)
	}

	root := os.Getenv("NODE_ROOT")
	if root == "" {
		root = filepath.Join(cwd, "test_dir")
	}
	initVFS(root)
	defer func() { _ = globalFS.Close() }()

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

	// Load extractor plugins from EMDEX_PLUGIN_DIR (default: ./plugins/).
	// EMDEX_PLUGIN_ENABLED=false disables the plugin system entirely.
	if os.Getenv("EMDEX_PLUGIN_ENABLED") != "false" {
		pluginDir := os.Getenv("EMDEX_PLUGIN_DIR")
		if pluginDir == "" {
			pluginDir = filepath.Join(cwd, "plugins")
		}
		if plugins, loadErr := plugin.LoadPlugins(pluginDir); loadErr != nil {
			log.Printf("[plugin] Load error from %s: %v", pluginDir, loadErr)
		} else if len(plugins) > 0 {
			pipelineCfg.Plugins = plugins
			log.Printf("[plugin] %d plugin(s) active for indexing", len(plugins))
		}
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

	// Closure that delegates to the extracted search.DeletePointsByPath,
	// capturing the global context, client, and collection name.
	deletePoints := func(path string) error {
		return search.DeletePointsByPath(globalCtx, globalPointsClient, globalCfg.CollectionName, path)
	}

	if globalCfg.NodeType == "local" {
		w, _ := watcher.New(root, func(ev watcher.FileEvent) error {
			// During the startup walk, skip files the walker already indexed
			// to prevent duplicate work from the watcher firing on the same paths.
			if walkComplete.Load() == 0 {
				if _, alreadySeen := walkSeen.Load(ev.Path); alreadySeen {
					log.Printf("[node] Watcher skipping %s (already indexed by startup walk)", ev.Path)
					return nil
				}
			}
			content, _ := os.ReadFile(ev.Path)
			points := indexer.IndexDataToPoints(ev.Path, content, pipelineCfg)
			if len(points) > 0 {
				_, err = globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
					CollectionName: globalCfg.CollectionName,
					Points:         points,
				})
				if err != nil && globalQueue != nil {
					_ = globalQueue.Enqueue(points)
				}
				return err
			}
			return nil
		}, deletePoints)
		if w != nil {
			globalWorkerHeartbeat = w.Heartbeat
			go w.Start()
		}
	} else {
		cache, _ := watcher.NewMetadataCache(filepath.Join(cacheDir, "emdex_cache.db"))
		if cache != nil {
			p := watcher.NewPoller(
				globalFS,
				root,
				cache,
				60*time.Second,
				func(path string, content []byte) error {
					points := indexer.IndexDataToPoints(path, content, pipelineCfg)
					if len(points) > 0 {
						_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
							CollectionName: globalCfg.CollectionName,
							Points:         points,
						})
						if err != nil && globalQueue != nil {
							_ = globalQueue.Enqueue(points)
						}
						return err
					}
					return nil
				},
				deletePoints,
			)
			globalWorkerHeartbeat = p.Heartbeat
			go p.Start()
		}
	}

	go func() {
		log.Printf("[node] Startup walk starting: root=%s vfs=%s", root, globalCfg.NodeType)
		idx := indexer.NewIndexer(globalFS)
		var batch []*qdrant.PointStruct
		flush := func() {
			if len(batch) == 0 { return }
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil {
				if globalQueue != nil {
					if qerr := globalQueue.Enqueue(batch); qerr != nil {
						log.Printf("[node] Walk flush: Qdrant upsert failed and queue enqueue failed: upsert_err=%v queue_err=%v (batch_size=%d — points lost)", err, qerr, len(batch))
					} else {
						log.Printf("[node] Walk flush: Qdrant upsert failed, queued for retry: err=%v (batch_size=%d)", err, len(batch))
					}
				} else {
					log.Printf("[node] Walk flush: Qdrant upsert failed and no queue available: err=%v (batch_size=%d — points lost)", err, len(batch))
				}
			}
			batch = nil
		}

		stats, walkErr := idx.Walk(root, func(path string, isDir bool, content []byte) error {
			// Record the path so the watcher won't re-index it during startup.
			walkSeen.Store(path, struct{}{})

			points := indexer.IndexDataToPoints(path, content, pipelineCfg)
			for _, p := range points {
				batch = append(batch, p)
				if len(batch) >= 100 { flush() }
			}
			return nil
		})
		flush()

		if walkErr != nil {
			log.Printf("[node] Startup walk failed: root=%s err=%v — watcher will handle new events", root, walkErr)
		}

		// Signal that the startup walk is complete. The watcher will now
		// process all events normally. Clear the seen cache to free memory.
		walkComplete.Store(1)
		walkSeen.Range(func(key, _ any) bool {
			walkSeen.Delete(key)
			return true
		})
		log.Printf("[node] Startup walk complete: root=%s indexed=%d skipped=%d dirs_skipped=%d — watcher now live",
			root, stats.FilesIndexed, stats.FilesSkipped, stats.DirsSkipped)
		// Notify gateway of walk completion (Phase 33).
		go reportIndexingComplete(globalCfg.GatewayURL, globalCfg.NodeID,
			globalCfg.Namespace, globalCfg.GatewayAuthKey, stats)
	}()

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

	health.StartServer(health.ServerConfig{
		QdrantConn:      conn,
		WorkerHeartbeat: globalWorkerHeartbeat,
	})
}

// contextModel returns the model to use for contextual retrieval context generation.
// Priority: EMDEX_CONTEXT_MODEL → EMDEX_LLM_MODEL → gemini-3-flash-preview.
func contextModel() string {
	if m := os.Getenv("EMDEX_CONTEXT_MODEL"); m != "" {
		return m
	}
	if m := os.Getenv("EMDEX_LLM_MODEL"); m != "" {
		return m
	}
	return "gemini-3-flash-preview"
}

// parseIntEnv parses an environment variable as an integer, returning def if unset or invalid.
func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// parseFloatEnv parses an environment variable as a float64, returning def if unset or invalid.
func parseFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

func initVFS(root string) {
	var err error
	switch globalCfg.NodeType {
	case "smb":
		globalFS, err = vfs.NewSMBFileSystem(globalCfg.SMBHost, globalCfg.SMBUser, globalCfg.SMBPass, globalCfg.SMBShare)
		if err == nil {
			log.Printf("[node] SMB VFS initialized: host=%s share=%s", globalCfg.SMBHost, globalCfg.SMBShare)
		}
	case "sftp":
		globalFS, err = vfs.NewSFTPFileSystem(globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser, globalCfg.SFTPPass)
		if err == nil {
			log.Printf("[node] SFTP VFS initialized: host=%s port=%s user=%s", globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser)
		}
	case "nfs":
		globalFS, err = vfs.NewNFSFileSystem(globalCfg.NFSHost, globalCfg.NFSPath)
		if err == nil {
			log.Printf("[node] NFS VFS initialized: host=%s path=%s", globalCfg.NFSHost, globalCfg.NFSPath)
		}
	case "s3":
		globalFS, err = vfs.NewS3FileSystem(globalCfg.S3Endpoint, globalCfg.S3AccessKey, globalCfg.S3SecretKey, globalCfg.S3Bucket, globalCfg.S3UseSSL)
		if err == nil {
			log.Printf("[node] S3 VFS initialized: endpoint=%s bucket=%s prefix=%q", globalCfg.S3Endpoint, globalCfg.S3Bucket, globalCfg.S3Prefix)
		}
	default:
		globalFS = &vfs.OSFileSystem{Root: root}
		log.Printf("[node] Local VFS initialized: root=%s", root)
	}
	if err != nil { panic(err) }
}
