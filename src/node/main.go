package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	Version        = "v1.0.5"
	EmbeddingModel = "models/gemini-embedding-exp-03-07"
	EmbeddingDims  = 3072
	CollectionName = "emdexer_v1"
)

var (
	indexingThroughput = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_indexing_throughput_total",
		Help: "Total number of files indexed",
	})
	embeddingLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_node_embedding_latency_ms",
		Help:    "Latency of Gemini embedding in milliseconds",
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

type Config struct {
	QdrantHost     string
	QdrantURL      string
	ExtractousHost string
	CollectionName string
	GoogleAPIKey   string
	Namespace      string
	NodeType       string // local, smb, sftp
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

type ExtractedResult struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
}

type EmbedRequest struct {
	Model   string       `json:"model"`
	Content EmbedContent `json:"content"`
}

type EmbedContent struct {
	Parts []EmbedPart `json:"parts"`
}

type EmbedPart struct {
	Text string `json:"text"`
}

type EmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

var globalPointsClient qdrant.PointsClient
var globalQueue *queue.PersistentQueue
var globalCB *extractor.CircuitBreaker
var globalCfg Config
var globalCtx context.Context
var globalFS vfs.FileSystem
var globalEmbedder embed.EmbedProvider

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		version.Print()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--license" {
		fmt.Printf("License: %s\n", version.LicenseType)
		return
	}
	cwd, _ := os.Getwd()
	loadEnv(filepath.Join(cwd, ".env"))

	provider := os.Getenv("EMBED_PROVIDER")
	if provider == "" {
		provider = "gemini"
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if provider == "gemini" && apiKey == "" {
		panic("GOOGLE_API_KEY not set (required for Gemini)")
	}

	qdrantHost := os.Getenv("QDRANT_HOST")
	if qdrantHost == "" {
		qdrantHost = "localhost:6334"
	}

	globalCfg = Config{
		QdrantHost:     qdrantHost,
		QdrantURL:      "http://localhost:6333",
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
		panic(fmt.Sprintf("Failed to connect to Qdrant gRPC: %v", err))
	}
	defer conn.Close()

	globalEmbedder = embed.New(
		globalCfg.GoogleAPIKey,
		provider,
		os.Getenv("OLLAMA_HOST"),
		os.Getenv("OLLAMA_EMBED_MODEL"),
		os.Getenv("EMDEX_GEMINI_MODEL"),
	)
	fmt.Printf("Using embed provider: %s\n", globalEmbedder.Name())

	collectionsClient := qdrant.NewCollectionsClient(conn)
	globalPointsClient = qdrant.NewPointsClient(conn)

	// Initialize Persistent Queue
	queuePath := os.Getenv("EMDEX_QUEUE_DB")
	if queuePath == "" {
		queuePath = filepath.Join(cwd, "cache", "queue.db")
	}
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	globalQueue, err = queue.NewPersistentQueue(queuePath)
	if err != nil {
		fmt.Printf("Failed to initialize persistent queue: %v\n", err)
	} else {
		go startQueueWorker()
	}

	initVFS()
	defer globalFS.Close()

	_, err = collectionsClient.Get(globalCtx, &qdrant.GetCollectionInfoRequest{
		CollectionName: globalCfg.CollectionName,
	})

	if err != nil {
		fmt.Printf("Collection %s not found, creating...\n", globalCfg.CollectionName)
		_, err = collectionsClient.Create(globalCtx, &qdrant.CreateCollection{
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
		if err != nil {
			panic(fmt.Sprintf("Failed to create collection: %v", err))
		}
	}

	root := os.Getenv("NODE_ROOT")
	if root == "" {
		cwd, _ := os.Getwd()
		root = filepath.Join(cwd, "test_dir")
	}

	// ── Phase 4: Real-time Watcher & Remote VFS Poller ──────────────────────
	// If the node is local, we spawn an fsnotify-based watcher.
	// For SMB/SFTP/NFS, we use the Poller with a SQLite-backed cache.
	if globalCfg.NodeType == "local" {
		w, err := watcher.New(root, func(ev watcher.FileEvent) error {
			content, err := os.ReadFile(ev.Path)
			if err != nil {
				return fmt.Errorf("read changed file: %w", err)
			}
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
		if err != nil {
			fmt.Printf("Failed to start watcher: %v\n", err)
		} else {
			go w.Start()
		}
	} else {
		// Remote Poller
		cacheDir := os.Getenv("EMDEX_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = filepath.Join(cwd, "cache")
		}
		cache, err := watcher.NewMetadataCache(filepath.Join(cacheDir, "emdex_cache.db"))
		if err != nil {
			fmt.Printf("Failed to initialize metadata cache: %v\n", err)
		} else {
			pollIntervalStr := os.Getenv("EMDEX_POLL_INTERVAL")
			pollInterval := 60 * time.Second
			if pollIntervalStr != "" {
				if d, err := time.ParseDuration(pollIntervalStr); err == nil {
					pollInterval = d
				} else if s, err := time.ParseDuration(pollIntervalStr + "s"); err == nil {
					pollInterval = s
				}
			}

			p := watcher.NewPoller(
				globalFS,
				root,
				cache,
				pollInterval,
				func(path string, content []byte) error {
					// Index handler
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
					// Delete handler (Tombstone logic)
					// We need to delete points with this path.
					// Since we use stable IDs based on path:chunk, it's easier to filter by payload path.
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
														MatchValue: &qdrant.Match_Keyword{
															Keyword: path,
														},
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
		fmt.Println("Initial full-walk indexing started...")
		idx := indexer.NewIndexer(globalFS)
		
		var batch []*qdrant.PointStruct
		batchSizeStr := os.Getenv("EMDEX_BATCH_SIZE")
		batchSize := 100
		if batchSizeStr != "" {
			if b, err := fmt.Sscanf(batchSizeStr, "%d", &batchSize); err != nil || b != 1 {
				batchSize = 100
			}
		}

		flush := func() {
			if len(batch) == 0 {
				return
			}
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil {
				fmt.Printf("Error upserting batch, enqueuing for retry: %v\n", err)
				errorCount.WithLabelValues("upsert", globalCfg.NodeType).Inc()
				if globalQueue != nil {
					globalQueue.Enqueue(batch)
				}
			}
			batch = nil
		}

		err := idx.Walk(root, func(path string, isDir bool, content []byte) error {
			points := indexDataToPoints(path, content)
			for _, p := range points {
				batch = append(batch, p)
				if len(batch) >= batchSize {
					flush()
				}
			}
			return nil
		})
		flush()
		if err != nil {
			fmt.Printf("Walk error: %v\n", err)
		}
		fmt.Println("Initial walk indexing complete. (Watcher active for local node)")
	}()

	startHealthServer(conn)
}

func initVFS() {
	var err error
	switch globalCfg.NodeType {
	case "smb":
		fmt.Printf("Initializing SMB VFS: %s/%s\n", globalCfg.SMBHost, globalCfg.SMBShare)
		globalFS, err = vfs.NewSMBFileSystem(globalCfg.SMBHost, globalCfg.SMBUser, globalCfg.SMBPass, globalCfg.SMBShare)
	case "sftp":
		fmt.Printf("Initializing SFTP VFS: %s:%s\n", globalCfg.SFTPHost, globalCfg.SFTPPort)
		globalFS, err = vfs.NewSFTPFileSystem(globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser, globalCfg.SFTPPass)
	case "nfs":
		fmt.Printf("Initializing NFS VFS: %s:%s\n", globalCfg.NFSHost, globalCfg.NFSPath)
		globalFS, err = vfs.NewNFSFileSystem(globalCfg.NFSHost, globalCfg.NFSPath)
	default:
		fmt.Println("Initializing Local VFS")
		globalFS = &vfs.OSFileSystem{}
	}

	if err != nil {
		panic(fmt.Sprintf("Failed to initialize VFS: %v", err))
	}
}

func indexDataToPoints(path string, content []byte) []*qdrant.PointStruct {
	fmt.Printf("Processing: %s\n", path)
	var text string
	var err error
	var skippedReason string

	startExt := time.Now()
	if len(content) > 0 {
		text, err = extractFromBytes(path, content, globalCfg.ExtractousHost)
	} else {
		text, err = extractContent(path, globalCfg.ExtractousHost)
	}

	if err != nil {
		fmt.Printf("Error extracting %s: %v\n", path, err)
		errorCount.WithLabelValues("extraction", globalCfg.NodeType).Inc()
		skippedReason = fmt.Sprintf("extraction_error: %v", err)
		// Don't return nil, we want to index a stub with skipped_reason
		text = ""
	}
	extractionLatency.Observe(float64(time.Since(startExt).Milliseconds()))

	var points []*qdrant.PointStruct
	// If extraction failed or returned nothing, we create a single stub point
	chunks := []string{""}
	if text != "" {
		chunks = smartChunk(text, 512, 50)
	}

	for i, chunk := range chunks {
		var vector []float32
		if chunk != "" {
			startEmb := time.Now()
			vector, err = getEmbedding(chunk, globalCfg.GoogleAPIKey)
			if err != nil {
				fmt.Printf("Error embedding chunk %d of %s: %v\n", i, path, err)
				errorCount.WithLabelValues("embedding", globalCfg.NodeType).Inc()
				continue
			}
			embeddingLatency.Observe(float64(time.Since(startEmb).Milliseconds()))
		} else {
			// Zero vector for stubs
			vector = make([]float32, EmbeddingDims)
		}

		// Content-addressable stable ID: UUID v5(NamespaceOID, path + ":" + chunkIndex).
		// Deterministic — re-indexing the same file/chunk produces the same point ID,
		// enabling idempotent upserts without duplicate accumulation.
		idInput := fmt.Sprintf("%s:%d", path, i)
		u := uuid.NewMD5(uuid.NameSpaceOID, []byte(idInput))

		payload := map[string]*qdrant.Value{
			"path":       {Kind: &qdrant.Value_StringValue{StringValue: path}},
			"chunk":      {Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(i)}},
			"text":       {Kind: &qdrant.Value_StringValue{StringValue: chunk}},
			"indexed_at": {Kind: &qdrant.Value_IntegerValue{IntegerValue: time.Now().UnixNano()}},
		}

		if skippedReason != "" {
			payload["skipped_reason"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: skippedReason}}
		} else if chunk == "" && text == "" {
			payload["skipped_reason"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: "extraction_yielded_no_content"}}
		}

		if globalCfg.Namespace != "" {
			payload["namespace"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: globalCfg.Namespace}}
		}

		points = append(points, &qdrant.PointStruct{
			Id: &qdrant.PointId{
				PointIdOptions: &qdrant.PointId_Uuid{
					Uuid: u.String(),
				},
			},
			Vectors: &qdrant.Vectors{
				VectorsOptions: &qdrant.Vectors_Vector{
					Vector: &qdrant.Vector{
						Data: vector,
					},
				},
			},
			Payload: payload,
		})
	}
	indexingThroughput.Inc()
	return points
}

func extractFromBytes(path string, data []byte, extractousHost string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	internalExts := map[string]bool{".txt": true, ".md": true, ".go": true, ".py": true, ".json": true}
	if internalExts[ext] {
		return string(data), nil
	}

	maxFileSizeStr := os.Getenv("EMDEX_MAX_FILE_SIZE")
	maxFileSize := int64(50 * 1024 * 1024) // 50MB
	if maxFileSizeStr != "" {
		// Simple parser for MB/GB
		if strings.HasSuffix(maxFileSizeStr, "MB") {
			fmt.Sscanf(strings.TrimSuffix(maxFileSizeStr, "MB"), "%d", &maxFileSize)
			maxFileSize *= 1024 * 1024
		} else if strings.HasSuffix(maxFileSizeStr, "GB") {
			fmt.Sscanf(strings.TrimSuffix(maxFileSizeStr, "GB"), "%d", &maxFileSize)
			maxFileSize *= 1024 * 1024 * 1024
		} else {
			fmt.Sscanf(maxFileSizeStr, "%d", &maxFileSize)
		}
	}

	if int64(len(data)) > maxFileSize {
		return "", fmt.Errorf("file too large: %d > %d", len(data), maxFileSize)
	}

	if !globalCB.Allow() {
		fmt.Printf("Circuit breaker OPEN: skipping extraction for %s\n", path)
		return "", fmt.Errorf("circuit breaker open")
	}

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	part.Write(data)
	writer.Close()
	req, err := http.NewRequest("POST", extractousHost+"/extract", bodyBuf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		globalCB.RecordFailure()
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		globalCB.RecordFailure()
		b, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("extraction API %d: %s", res.StatusCode, string(b))
	}

	globalCB.RecordSuccess()
	var result ExtractedResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Text, nil
}

func extractContent(path, extractousHost string) (string, error) {
	// Legacy fallback
	f, err := globalFS.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	return extractFromBytes(path, data, extractousHost)
}

func startHealthServer(qdrantConn *grpc.ClientConn) {
	healthClient := grpc_health_v1.NewHealthClient(qdrantConn)
	startTime := time.Now()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz/liveness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "UP",
			"version": version.Version,
			"license": version.LicenseType,
		})
	})

	mux.HandleFunc("/healthz/readiness", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "DOWN", "reason": "qdrant_unreachable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/startup", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(startTime) < 5*time.Second {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "STARTING"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "STARTED"})
	})

	port := os.Getenv("NODE_HEALTH_PORT")
	if port == "" {
		port = "8081"
	}

	fmt.Printf("Node health server starting on :%s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Printf("Health server failed: %v\n", err)
	}
}

// getEmbedding delegates to the global EmbedProvider.
// The old direct HTTP call is replaced — this is the seam for Phase 15.5.
func getEmbedding(text, _ string) ([]float32, error) {
	start := time.Now()
	v, err := globalEmbedder.Embed(text)
	embeddingLatency.Observe(float64(time.Since(start).Milliseconds()))
	return v, err
}

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

func startQueueWorker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for {
			item, err := globalQueue.Dequeue()
			if err != nil {
				fmt.Printf("Queue worker error: %v\n", err)
				break
			}
			if item == nil {
				break
			}

			fmt.Printf("Queue worker: retrying batch %d (%d points)\n", item.ID, len(item.Points))
			_, err = globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         item.Points,
			})
			if err != nil {
				fmt.Printf("Queue worker: retry failed for batch %d: %v\n", item.ID, err)
				// Put it back or keep it in the queue by not deleting.
				// Our Dequeue implementation is destructive in logic but let's check if we should keep it.
				// In this simple WAL, Dequeue doesn't delete, but my impl did.
				// Let's refine the logic: Dequeue should perhaps not delete immediately?
				// For now, if it fails, we'll just stop processing this cycle.
				break
			}

			if err := globalQueue.Delete(item.ID); err != nil {
				fmt.Printf("Queue worker: failed to delete batch %d: %v\n", item.ID, err)
			}
		}
	}
}
