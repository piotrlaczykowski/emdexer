package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extract"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/nodereg"
	"github.com/piotrlaczykowski/emdexer/health"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/safenet"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	DefaultEmbedDims = 3072
	CollectionName   = "emdexer_v1"
)

var EmbeddingDims uint64 = DefaultEmbedDims

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
}

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
	}

	if globalCfg.ExtractousHost == "" {
		globalCfg.ExtractousHost = "http://localhost:8000/extract"
	}
	if globalCfg.NodeType == "" {
		globalCfg.NodeType = "local"
	}
	if globalCfg.Namespace == "" {
		globalCfg.Namespace = "default"
	}

	globalCB = extractor.NewCircuitBreaker(5, 5*time.Minute)
	globalCtx = context.Background()
	conn, err := grpc.NewClient(globalCfg.QdrantHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	initVFS()
	defer func() { _ = globalFS.Close() }()

	// Create extract client and pipeline config after VFS, CB, and embedder are ready.
	extractClient := &extract.Client{
		CB:   globalCB,
		FS:   globalFS,
		HTTP: safenet.NewSafeClient(60 * time.Second),
	}

	pipelineCfg := indexer.PipelineConfig{
		Namespace:      globalCfg.Namespace,
		ExtractousHost: globalCfg.ExtractousHost,
		NodeType:       globalCfg.NodeType,
		Embedder:       globalEmbedder,
		Extract: func(path string, content []byte, host string) (string, error) {
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

	root := os.Getenv("NODE_ROOT")
	if root == "" {
		root = filepath.Join(cwd, "test_dir")
	}

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
		cacheDir := os.Getenv("EMDEX_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = filepath.Join(cwd, "cache")
		}
		_ = os.MkdirAll(cacheDir, 0700)
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
		idx := indexer.NewIndexer(globalFS)
		var batch []*qdrant.PointStruct
		flush := func() {
			if len(batch) == 0 { return }
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil && globalQueue != nil {
				_ = globalQueue.Enqueue(batch)
			}
			batch = nil
		}

		_ = idx.Walk(root, func(path string, isDir bool, content []byte) error {
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

		// Signal that the startup walk is complete. The watcher will now
		// process all events normally. Clear the seen cache to free memory.
		walkComplete.Store(1)
		walkSeen.Range(func(key, _ any) bool {
			walkSeen.Delete(key)
			return true
		})
		log.Println("[node] Startup walk complete — watcher now processing all events")
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

func initVFS() {
	var err error
	switch globalCfg.NodeType {
	case "smb":
		globalFS, err = vfs.NewSMBFileSystem(globalCfg.SMBHost, globalCfg.SMBUser, globalCfg.SMBPass, globalCfg.SMBShare)
	case "sftp":
		globalFS, err = vfs.NewSFTPFileSystem(globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser, globalCfg.SFTPPass)
	case "nfs":
		globalFS, err = vfs.NewNFSFileSystem(globalCfg.NFSHost, globalCfg.NFSPath)
	default:
		globalFS = &vfs.OSFileSystem{}
	}
	if err != nil { panic(err) }
}
