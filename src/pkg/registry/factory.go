package registry

import (
	"log"
	"os"
	"strings"
)

// NewRegistry returns a DBNodeRegistry if POSTGRES_URL is set,
// otherwise falls back to FileNodeRegistry.
//
// HA enforcement: if EMDEX_HA_MODE=true is set, the gateway MUST use PostgreSQL.
// Falling back to FileNodeRegistry in HA mode would cause split-brain across
// multiple gateway replicas (each would maintain its own local nodes.json).
func NewRegistry(dataFile string) NodeRegistry {
	haMode := strings.EqualFold(os.Getenv("EMDEX_HA_MODE"), "true")

	if dsn := os.Getenv("POSTGRES_URL"); dsn != "" {
		log.Printf("[registry] POSTGRES_URL detected — using PostgreSQL backend")
		reg, err := NewDBNodeRegistry(dsn)
		if err != nil {
			if haMode {
				log.Fatalf("[registry] FATAL: HA mode is enabled but PostgreSQL is unreachable: %v\n"+
					"  → A local nodes.json fallback would cause split-brain across gateway replicas.\n"+
					"  → Fix the POSTGRES_URL connection or disable HA mode (unset EMDEX_HA_MODE).", err)
			}
			log.Printf("[registry] WARNING: PostgreSQL init failed (%v) — falling back to FileNodeRegistry", err)
		} else {
			return reg
		}
	} else if haMode {
		log.Fatalf("[registry] FATAL: EMDEX_HA_MODE=true requires POSTGRES_URL to be set.\n" +
			"  → Set POSTGRES_URL to a shared PostgreSQL instance for multi-replica consistency.\n" +
			"  → Example: POSTGRES_URL=postgres://user:pass@db:5432/emdexer?sslmode=require")
	}

	log.Printf("[registry] Using FileNodeRegistry at %s", dataFile)
	return NewFileNodeRegistry(dataFile)
}
