package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"karte/internal/agent"
)

func main() {
	var dataDir string
	flag.StringVar(&dataDir, "data-dir", "", "Path to karte_data directory (default: auto-detect)")
	flag.Parse()

	// Auto-detect data directory if not provided
	if dataDir == "" {
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable path: %v", err)
		}
		exeDir := filepath.Dir(exePath)
		// Check if running inside .app bundle
		if filepath.Base(exeDir) == "MacOS" {
			contentsDir := filepath.Dir(exeDir)
			appBundleDir := filepath.Dir(contentsDir)
			dataDir = filepath.Join(filepath.Dir(appBundleDir), "karte_data")
		} else {
			dataDir = filepath.Join(exeDir, "karte_data")
		}
	}

	// Ensure data directory exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		log.Fatalf("Data directory does not exist: %s", dataDir)
	}

	// Setup logging to file
	logDir := filepath.Join(dataDir, "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	// Setup log rotation: check file size (rotate if > 10MB)
	logFile := filepath.Join(logDir, "agent.log")
	if info, err := os.Stat(logFile); err == nil {
		if info.Size() > 10*1024*1024 {
			// Rotate: rename to agent.log.YYYYMMDD-HHMMSS
			backupName := logFile + "." + time.Now().Format("20060102-150405")
			if err := os.Rename(logFile, backupName); err != nil {
				log.Printf("Warning: Failed to rotate log file: %v", err)
			}
		}
	}

	// Open log file for writing
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer file.Close()

	// Write to both file and stdout
	multiWriter := io.MultiWriter(os.Stdout, file)
	log.SetOutput(multiWriter)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create agent instance
	ag, err := agent.NewAgent(ctx, dataDir)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Start agent
	if err := ag.Start(); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
	defer ag.Stop()

	log.Printf("karte-agent started (dataDir=%s, logFile=%s)", dataDir, logFile)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down karte-agent...")
}
