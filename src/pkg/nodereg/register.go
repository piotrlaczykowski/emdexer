package nodereg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// NodeConfig holds the information needed to register with the gateway.
type NodeConfig struct {
	GatewayURL     string
	GatewayAuthKey string
	NodeID         string
	CollectionName string
	Namespace      string
	NodeType       string
}

// Register sends a registration/heartbeat payload to the gateway.
// Uses plain http.Client (not safenet) because the gateway is on the internal
// network, and safenet blocks private/loopback IPs by design.
func Register(cfg NodeConfig) {
	if cfg.GatewayURL == "" {
		return
	}

	healthPort := os.Getenv("NODE_HEALTH_PORT")
	if healthPort == "" {
		healthPort = "8081"
	}
	nodeURL := os.Getenv("EMDEX_NODE_URL")
	if nodeURL == "" {
		hostname, _ := os.Hostname()
		nodeURL = fmt.Sprintf("http://%s:%s", hostname, healthPort)
	}

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	payload := map[string]interface{}{
		"id":            cfg.NodeID,
		"url":           nodeURL,
		"collections":   []string{cfg.CollectionName},
		"namespaces":    []string{namespace},
		"protocol":      cfg.NodeType,
		"health_status": "healthy",
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", cfg.GatewayURL+"/nodes/register", bytes.NewReader(body))
	if err != nil {
		log.Printf("[node] Registration request build failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.GatewayAuthKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.GatewayAuthKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[node] Registration failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	log.Printf("[node] Registered with gateway %s (status %d, id=%s)", cfg.GatewayURL, resp.StatusCode, cfg.NodeID)
}

// StartHeartbeatLoop re-registers with the gateway every 60 seconds.
func StartHeartbeatLoop(cfg NodeConfig) {
	if cfg.GatewayURL == "" {
		return
	}
	ticker := time.NewTicker(60 * time.Second)
	for range ticker.C {
		Register(cfg)
	}
}
