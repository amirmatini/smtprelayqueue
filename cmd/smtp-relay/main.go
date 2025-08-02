// Copyright (c) 2024 SMTP Relay Contributors
// Licensed under the MIT License

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"smtp-relay/internal/config"
	"smtp-relay/internal/relay"
	"smtp-relay/internal/storage"
)

// ensureDirectories creates necessary directories if they don't exist
func ensureDirectories(cfg *config.Config) error {
	// Create messages directory if using file storage
	if cfg.Storage.Type == "file" && cfg.Storage.File.Path != "" {
		if err := os.MkdirAll(cfg.Storage.File.Path, 0755); err != nil {
			return fmt.Errorf("failed to create messages directory: %v", err)
		}
		log.Printf("Messages directory ensured: %s", cfg.Storage.File.Path)
	}

	// Create logs directory if specified
	if cfg.Logging.File != "" {
		logDir := filepath.Dir(cfg.Logging.File)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create logs directory: %v", err)
		}
		log.Printf("Logs directory ensured: %s", logDir)
	}

	return nil
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Ensure necessary directories exist
	if err := ensureDirectories(cfg); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	// Initialize storage
	store, err := storage.New(cfg.Storage)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Create and start relay
	r, err := relay.New(cfg, store)
	if err != nil {
		log.Fatalf("Failed to create relay: %v", err)
	}

	// Start the relay
	if err := r.Start(); err != nil {
		log.Fatalf("Failed to start relay: %v", err)
	}

	fmt.Printf("SMTP Relay started on %s:%d\n", cfg.Incoming.Host, cfg.Incoming.Port)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down SMTP Relay...")
	if err := r.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
