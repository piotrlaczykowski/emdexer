package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	indexingThroughput = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_indexing_throughput_total",
		Help: "Total number of files indexed",
	})
	embeddingLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_node_embedding_latency_ms",
		Help:    "Latency of embedding in milliseconds",
		Buckets: []float64{100, 200, 500, 1000, 2000, 5000},
	})
	extractionLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_node_extraction_latency_ms",
		Help:    "Latency of content extraction in milliseconds",
		Buckets: []float64{50, 100, 250, 500, 1000, 2500, 5000, 10000},
	})
	errorCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_node_errors_total",
		Help: "Total number of errors during indexing",
	}, []string{"type", "ext"})
)

var globalPointsClient qdrant.PointsClient
var globalQueue *queue.PersistentQueue
var globalCfg Config
var globalCtx context.Context
var globalEmbedder embed.EmbedProvider

func smartChunk(text string, size, overlap int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var chunks []string
	step := size - overlap
	if step <= 0 {
		step = 1
	}
	for i := 0; i < len(words); i += step {
		end := i + size
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		version.Print()
		return
	}
	cwd, _ := os.Getwd()
	loadEnv(filepath.Join(cwd, ".env"))

	apiKey := os.Getenv("GOOGLE_API_KEY")
	qdrantHost := os.Getenv("QDRANT_HOST")
	if qdrantHost == "" {
		qdrantHost = "localhost:6334"
	}

	globalCfg = Config{
		QdrantHost:     qdrantHost,
		ExtractousHost: os.Getenv("EMDEX_EXTRACTOUS_URL"),
		CollectionName: CollectionName,
		GoogleAPIKey:   apiKey,
		Namespace:      os.Getenv("EMDEX_NAMESPACE"),
		NodeType:       os.Getenv("NODE_TYPE"),
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
		S3Endpoint:     os.Getenv("EMDEX_S3_ENDPOINT"),
		S3Bucket:       os.Getenv("EMDEX_S3_BUCKET"),
		S3AccessKey:    os.Getenv("EMDEX_S3_ACCESS_KEY"),
		S3SecretKey:    os.Getenv("EMDEX_S3_SECRET_KEY"),
		S3Region:       os.Getenv("EMDEX_S3_REGION"),
		S3UseSSL:       os.Getenv("EMDEX_S3_USE_SSL"),
		S3UsePathStyle: os.Getenv("EMDEX_S3_USE_PATH_STYLE") == "true",
		S3Prefix:       os.Getenv("EMDEX_S3_PREFIX"),
		S3PollInterval: os.Getenv("EMDEX_S3_POLL_INTERVAL"),
	}

	if globalCfg.ExtractousHost == "" {
		globalCfg.ExtractousHost = "http://localhost:8000/extract"
	}
	if globalCfg.NodeType == "" {
		globalCfg.NodeType = "local"
	}

	globalCB = extractor.NewCircuitBreaker(5, 5*time.Minute)
	globalCtx = context.Background()
	conn, err := grpc.Dial(globalCfg.QdrantHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()

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
	os.MkdirAll(filepath.Dir(queuePath), 0700)
	globalQueue, err = queue.NewPersistentQueue(queuePath)
	if err == nil {
		go startQueueWorker()
	}

	initVFS(globalCfg)
	defer globalFS.Close()

	_, err = collectionsClient.Get(globalCtx, &qdrant.GetCollectionInfoRequest{
		CollectionName: globalCfg.CollectionName,
	})

	if err != nil {
		collectionsClient.Create(globalCtx, &qdrant.CreateCollection{
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

	traversalRoot := root
	if globalCfg.NodeType == "s3" {
		traversalRoot = "."
	}

	if globalCfg.NodeType == "local" {
		w, _ := watcher.New(root, func(ev watcher.FileEvent) error {
			content, _ := os.ReadFile(ev.Path)
			points := indexDataToPoints(ev.Path, content)
			if len(points) > 0 {
				_, err = globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
					CollectionName: globalCfg.CollectionName,
					Points:         points,
				})
				if err != nil && globalQueue != nil {
					globalQueue.Enqueue(points)
				}
				return err
			}
			return nil
		})
		if w != nil {
			go w.Start()
		}
	} else if globalCfg.NodeType == "smb" || globalCfg.NodeType == "sftp" || globalCfg.NodeType == "nfs" || globalCfg.NodeType == "s3" {
		cacheDir := os.Getenv("EMDEX_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = filepath.Join(cwd, "cache")
		}
		os.MkdirAll(cacheDir, 0700)
		cache, _ := watcher.NewMetadataCache(filepath.Join(cacheDir, "emdex_cache.db"))

		pollInterval := 60 * time.Second
		if globalCfg.NodeType == "s3" {
			if globalCfg.S3PollInterval != "" {
				if d, err := time.ParseDuration(globalCfg.S3PollInterval); err == nil {
					pollInterval = d
				}
			} else {
				pollInterval = 5 * time.Minute
			}
		}

		if cache != nil {
			p := watcher.NewPoller(
				globalFS,
				traversalRoot,
				cache,
				pollInterval,
				func(path string, content []byte) error {
					points := indexDataToPoints(path, content)
					if len(points) > 0 {
						_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
							CollectionName: globalCfg.CollectionName,
							Points:         points,
						})
						if err != nil && globalQueue != nil {
							globalQueue.Enqueue(points)
						}
						return err
					}
					return nil
				},
				func(path string) error {
					_, err := globalPointsClient.Delete(globalCtx, &qdrant.DeletePoints{
						CollectionName: globalCfg.CollectionName,
						Points: &qdrant.PointsSelector{
							PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
								Filter: &qdrant.Filter{
									Must: []*qdrant.Condition{
										{
											ConditionOneOf: &qdrant.Condition_Field{
												Field: &qdrant.FieldCondition{
													Key: "path",
													Match: &qdrant.Match{
														MatchValue: &qdrant.Match_Keyword{Keyword: path},
													},
												},
											},
										},
									},
								},
							},
						},
					})
					return err
				},
			)
			go p.Start()
		}
	}

	go func() {
		idx := indexer.NewIndexer(globalFS)
		var batch []*qdrant.PointStruct
		flush := func() {
			if len(batch) == 0 {
				return
			}
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil && globalQueue != nil {
				globalQueue.Enqueue(batch)
			}
			batch = nil
		}

		idx.Walk(traversalRoot, func(path string, isDir bool, content []byte) error {
			points := indexDataToPoints(path, content)
			for _, p := range points {
				batch = append(batch, p)
				if len(batch) >= 100 {
					flush()
				}
			}
			return nil
		})
		flush()
	}()

	startHealthServer(conn)
}
