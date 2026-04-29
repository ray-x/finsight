package logger

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	logFile *os.File
	lg      *log.Logger
)

// Init opens the log file for writing. If path is empty, logging is disabled.
func Init(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	logFile = f
	lg = log.New(f, "", 0)
	return nil
}

// Close closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
		lg = nil
	}
}

// Log writes a timestamped message to the log file.
func Log(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if lg == nil {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	lg.Printf("[%s] %s", ts, fmt.Sprintf(format, args...))
}
