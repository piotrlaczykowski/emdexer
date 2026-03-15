package main

import (
	"log"
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
	EmbeddingDims  = 3072
	CollectionName = "emdexer_v1"
	ProjectNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
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

type Config struct {
	QdrantHost     string
	ExtractousHost string
	CollectionName string
	GoogleAPIKey   string
	Namespace      string
	NodeType       string
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

	initVFS()
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
	} else {
		cacheDir := os.Getenv("EMDEX_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = filepath.Join(cwd, "cache")
		}
		os.MkdirAll(cacheDir, 0700)
		cache, _ := watcher.NewMetadataCache(filepath.Join(cacheDir, "emdex_cache.db"))
		if cache != nil {
			p := watcher.NewPoller(
				globalFS,
				root,
				cache,
				60*time.Second,
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
			if len(batch) == 0 { return }
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil && globalQueue != nil {
				globalQueue.Enqueue(batch)
			}
			batch = nil
		}

		idx.Walk(root, func(path string, isDir bool, content []byte) error {
			points := indexDataToPoints(path, content)
			for _, p := range points {
				batch = append(batch, p)
				if len(batch) >= 100 { flush() }
			}
			return nil
		})
		flush()
	}()

	startHealthServer(conn)
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

func indexDataToPoints(path string, content []byte) []*qdrant.PointStruct {
	var text string
	var err error
	startExt := time.Now()
	if len(content) > 0 {
		text, err = extractFromBytes(path, content, globalCfg.ExtractousHost)
	} else {
		text, err = extractContent(path, globalCfg.ExtractousHost)
	}
	extractionLatency.Observe(float64(time.Since(startExt).Milliseconds()))

	if err != nil {
		errorCount.WithLabelValues("extraction", globalCfg.NodeType).Inc()
		text = ""
	}

	var points []*qdrant.PointStruct
	chunks := []string{""}
	if text != "" {
		chunks = smartChunk(text, 512, 50)
	}

	for i, chunk := range chunks {
		var vector []float32
		if chunk != "" {
			startEmb := time.Now()
			vector, err = globalEmbedder.Embed(chunk)
			if err != nil {
				errorCount.WithLabelValues("embedding", globalCfg.NodeType).Inc()
				continue
			}
			embeddingLatency.Observe(float64(time.Since(startEmb).Milliseconds()))
		} else {
			vector = make([]float32, EmbeddingDims)
		}

		ns := uuid.MustParse(ProjectNamespace)
		idInput := fmt.Sprintf("%s:%d", path, i)
		u := uuid.NewSHA1(ns, []byte(idInput))

		payload := map[string]*qdrant.Value{
			"path":       {Kind: &qdrant.Value_StringValue{StringValue: path}},
			"chunk":      {Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(i)}},
			"text":       {Kind: &qdrant.Value_StringValue{StringValue: chunk}},
			"indexed_at": {Kind: &qdrant.Value_IntegerValue{IntegerValue: time.Now().UnixNano()}},
		}
		if globalCfg.Namespace != "" {
			payload["namespace"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: globalCfg.Namespace}}
		}

		points = append(points, &qdrant.PointStruct{
			Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Uuid{Uuid: u.String()}},
			Vectors: &qdrant.Vectors{VectorsOptions: &qdrant.Vectors_Vector{Vector: &qdrant.Vector{Data: vector}}},
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

	if !globalCB.Allow() { return "", fmt.Errorf("cb open") }

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	part, _ := writer.CreateFormFile("file", filepath.Base(path))
	part.Write(data)
	writer.Close()
	req, _ := http.NewRequest("POST", extractousHost+"/extract", bodyBuf)
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
		return "", fmt.Errorf("extraction API %d", res.StatusCode)
	}

	globalCB.RecordSuccess()
	var result ExtractedResult
	json.NewDecoder(res.Body).Decode(&result)
	return result.Text, nil
}

func extractContent(path, extractousHost string) (string, error) {
	f, err := globalFS.Open(path)
	if err != nil { return "", err }
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
		json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/readiness", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "DOWN"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/startup", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(startTime) < 5*time.Second {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "STARTING"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "STARTED"})
	})

	port := os.Getenv("NODE_HEALTH_PORT")
	if port == "" {
		port = "8081"
	}
	addr := ":" + port
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("health server error: %v", err)
	}
}

func smartChunk(text string, size, overlap int) []string {
	words := strings.Fields(text)
	if len(words) == 0 { return nil }
	var chunks []string
	step := size - overlap
	if step <= 0 { step = 1 }
	for i := 0; i < len(words); i += step {
		end := i + size
		if end > len(words) { end = len(words) }
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) { break }
	}
	return chunks
}

func startQueueWorker() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		for {
			item, _ := globalQueue.Dequeue()
			if item == nil { break }
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         item.Points,
			})
			if err == nil { globalQueue.Delete(item.ID) } else { break }
		}
	}
}
