package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/plugin"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/watcher"

	"github.com/qdrant/go-client/qdrant"
)

func startIndexing(root, cwd string, pipelineCfg indexer.PipelineConfig, indexWorkers int) {
	queuePath := os.Getenv("EMDEX_QUEUE_DB")
	if queuePath == "" {
		queuePath = filepath.Join(cwd, "cache", "queue.db")
	}
	_ = os.MkdirAll(filepath.Dir(queuePath), 0700)
	var err error
	globalQueue, err = queue.NewPersistentQueue(queuePath)
	if err == nil {
		go queue.StartWorker(globalQueue, globalPointsClient, globalCfg.CollectionName, globalCtx)
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

	pipelineCfg.Ctx = globalCtx

	// Closure that delegates to the extracted search.DeletePointsByPath,
	// capturing the global context, client, and collection name.
	deletePoints := func(path string) error {
		return search.DeletePointsByPath(globalCtx, globalPointsClient, globalCfg.CollectionName, path)
	}

	// Hoist cacheDir so it is available for both migration check and poller setup.
	cacheDir := os.Getenv("EMDEX_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(cwd, "cache")
	}
	_ = os.MkdirAll(cacheDir, 0700)

	if globalCfg.NodeType == "local" {
		upsertFn := func(pts []*qdrant.PointStruct) {
			_, uErr := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         pts,
			})
			if uErr != nil && globalQueue != nil {
				_ = globalQueue.Enqueue(pts)
			}
		}
		batcher := &microBatcher{
			flushFn:  upsertFn,
			window: 200 * time.Millisecond,
		}
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
				batcher.add(points)
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
		// Read Qdrant flush batch size — default 500 (up from 100) to reduce gRPC overhead.
		batchSize := 500
		if v := os.Getenv("EMDEX_BATCH_SIZE"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 5000 {
				batchSize = n
			}
		}

		log.Printf("[node] Startup walk starting: root=%s vfs=%s workers=%d batchSize=%d",
			root, globalCfg.NodeType, indexWorkers, batchSize)

		// Build walk config with feature flags
		walkCfg := indexer.BuildWalkConfig(
			globalCfg.WhisperEnabled,
			globalCfg.VisionEnabled,
			globalCfg.FrameEnabled,
			globalCfg.EnableOCR,
		)
		log.Printf("[node] walk skip extensions: %d types (audio=%v vision=%v frame=%v ocr=%v)",
			len(walkCfg.SkipExts),
			!globalCfg.WhisperEnabled, !globalCfg.VisionEnabled,
			!globalCfg.FrameEnabled, globalCfg.EnableOCR,
		)
		if len(walkCfg.ExcludePaths) > 0 {
			log.Printf("[node] walk exclude paths: %v", walkCfg.ExcludePaths)
		}

		idx := indexer.NewIndexer(globalFS, walkCfg)

		// Worker pool for parallel file processing.
		type workItem struct {
			path    string
			content []byte
		}

		jobs := make(chan workItem, indexWorkers*4)
		var mu sync.Mutex
		var batch []*qdrant.PointStruct

		flush := func() {
			mu.Lock()
			if len(batch) == 0 {
				mu.Unlock()
				return
			}
			toFlush := batch
			batch = nil
			mu.Unlock()

			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         toFlush,
			})
			if err != nil {
				if globalQueue != nil {
					if qerr := globalQueue.Enqueue(toFlush); qerr != nil {
						log.Printf("[node] Walk flush: Qdrant upsert failed and queue enqueue failed: upsert_err=%v queue_err=%v (batch_size=%d — points lost)", err, qerr, len(toFlush))
					} else {
						log.Printf("[node] Walk flush: Qdrant upsert failed, queued for retry: err=%v (batch_size=%d)", err, len(toFlush))
					}
				} else {
					log.Printf("[node] Walk flush: Qdrant upsert failed and no queue available: err=%v (batch_size=%d — points lost)", err, len(toFlush))
				}
			}
		}

		var wg sync.WaitGroup
		for w := 0; w < indexWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range jobs {
					points := indexer.IndexDataToPoints(item.path, item.content, pipelineCfg)
					if len(points) == 0 {
						continue
					}
					mu.Lock()
					batch = append(batch, points...)
					shouldFlush := len(batch) >= batchSize
					mu.Unlock()
					if shouldFlush {
						flush()
					}
				}
			}()
		}

		stats, walkErr := idx.Walk(root, func(path string, isDir bool, content []byte) error {
			// Record the path so the watcher won't re-index it during startup.
			walkSeen.Store(path, struct{}{})
			jobs <- workItem{path: path, content: content}
			return nil
		})

		close(jobs)
		wg.Wait()
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
}

// microBatcher coalesces rapid single-file watcher events into small batches
// before upserting to Qdrant. A timer fires after window of inactivity.
type microBatcher struct {
	mu      sync.Mutex
	pending []*qdrant.PointStruct
	timer   *time.Timer
	flushFn func([]*qdrant.PointStruct)
	window  time.Duration
}

func (mb *microBatcher) add(points []*qdrant.PointStruct) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.pending = append(mb.pending, points...)
	if mb.timer == nil {
		mb.timer = time.AfterFunc(mb.window, mb.flush)
	}
}

func (mb *microBatcher) flush() {
	mb.mu.Lock()
	batch := mb.pending
	mb.pending = nil
	mb.timer = nil
	mb.mu.Unlock()
	if len(batch) > 0 {
		mb.flushFn(batch)
	}
}
