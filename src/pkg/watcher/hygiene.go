package watcher

import (
	"context"
	"log"
	"time"
)

// PurgeStale removes file_cache rows whose last_seen is older than retention.
// Returns the row count deleted. retention <= 0 is a no-op.
func (c *MetadataCache) PurgeStale(retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-retention).Unix()
	res, err := c.db.Exec(
		`DELETE FROM file_cache WHERE last_seen IS NOT NULL AND last_seen < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum reclaims free pages from the SQLite file. Safe under WAL mode.
func (c *MetadataCache) Vacuum(ctx context.Context) error {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "VACUUM")
	return err
}

// StartHygiene spawns a background goroutine that periodically purges stale
// rows and VACUUMs. Returns a stop function (cancel). retention <= 0 skips
// purge; vacuumInterval <= 0 skips vacuum.
func (c *MetadataCache) StartHygiene(
	ctx context.Context,
	retention time.Duration,
	purgeInterval, vacuumInterval time.Duration,
) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		var purgeTicker, vacuumTicker *time.Ticker
		if purgeInterval > 0 && retention > 0 {
			purgeTicker = time.NewTicker(purgeInterval)
			defer purgeTicker.Stop()
		}
		if vacuumInterval > 0 {
			vacuumTicker = time.NewTicker(vacuumInterval)
			defer vacuumTicker.Stop()
		}
		if retention > 0 {
			if n, err := c.PurgeStale(retention); err != nil {
				log.Printf("[cache] purge-stale at startup error: %v", err)
			} else if n > 0 {
				log.Printf("[cache] purge-stale at startup removed %d rows", n)
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tickerC(purgeTicker):
				if n, err := c.PurgeStale(retention); err != nil {
					log.Printf("[cache] purge-stale error: %v", err)
				} else if n > 0 {
					log.Printf("[cache] purge-stale removed %d rows", n)
				}
			case <-tickerC(vacuumTicker):
				if err := c.Vacuum(ctx); err != nil {
					log.Printf("[cache] vacuum error: %v", err)
				} else {
					log.Printf("[cache] vacuum complete")
				}
			}
		}
	}()
	return cancel
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}
