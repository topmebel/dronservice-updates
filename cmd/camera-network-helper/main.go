package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"DronService/internal/cameranetwork"
)

func main() {
	dataDir := strings.TrimSpace(os.Getenv("DRONSERVICE_DATA_DIR"))
	if dataDir == "" {
		dataDir = "/var/lib/dronservice"
	}
	allowed := cameranetwork.ParseAllowedInterfaces(os.Getenv("DRONSERVICE_CAMERA_NETWORK_INTERFACES"))
	if len(allowed) == 0 {
		log.Fatal("DRONSERVICE_CAMERA_NETWORK_INTERFACES has no approved interfaces")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := (cameranetwork.Helper{DataDir: dataDir, AllowedInterfaces: allowed}).Process(ctx); err != nil {
		log.Fatalf("process camera network request: %v", err)
	}
}
