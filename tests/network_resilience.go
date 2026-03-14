package main

import (
	"context"
	"fmt"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/connectivity"
)

func main() {
	qdrantHost := "localhost:6334" // Default if not overridden
	
	conn, err := grpc.Dial(qdrantHost, 
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Printf("Dial failed: %v\n", err)
		return
	}
	defer conn.Close()

	// Simulation: Check state over time
	fmt.Println("Monitoring connection state for 10 seconds...")
	for i := 0; i < 10; i++ {
		state := conn.GetState()
		fmt.Printf("[%d] Connection state: %v\n", i, state)
		
		if state == connectivity.Ready {
			client := qdrant.NewCollectionsClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			_, err := client.List(ctx, &qdrant.ListCollectionsRequest{})
			cancel()
			if err != nil {
				fmt.Printf("RPC Error: %v\n", err)
			} else {
				fmt.Println("RPC Success: ListCollections")
			}
		}
		
		time.Sleep(1 * time.Second)
	}
}
