package registry

import (
	"database/sql"
	"testing"
)

// TestRegistryReadReplica_FallbackOnError verifies that OpenReplica with an
// invalid DSN logs a warning but leaves replicaDB nil so all queries continue
// to use the primary — never fatal.
func TestRegistryReadReplica_FallbackOnError(t *testing.T) {
	// Build a registry with a non-nil primary db stub so we can call OpenReplica.
	// We use an in-memory sqlite3 substitute via a fake db that's already closed,
	// but since sql.Open is lazy we can open postgres with a dummy DSN and never Ping.
	// The simplest approach: construct the struct directly (white-box, same package).
	r := &DBNodeRegistry{db: &sql.DB{}} // db will never be used in this test

	// Pass an invalid DSN — Ping will fail → replica must remain nil.
	r.OpenReplica("postgres://invalid-host-that-does-not-exist:5432/db?connect_timeout=1")

	if r.replicaDB != nil {
		t.Fatal("expected replicaDB to remain nil after failed ping, but it was set")
	}

	// readDB must fall back to primary.
	if r.readDB() != r.db {
		t.Fatal("readDB should return primary when replicaDB is nil")
	}
}

// TestRegistryReadReplica_UsesReplicaWhenSet verifies that readDB() returns
// the replica connection when one has been successfully attached.
func TestRegistryReadReplica_UsesReplicaWhenSet(t *testing.T) {
	primary := &sql.DB{}
	replica := &sql.DB{}
	r := &DBNodeRegistry{db: primary, replicaDB: replica}

	if r.readDB() != replica {
		t.Fatal("readDB should return replicaDB when set")
	}
}
