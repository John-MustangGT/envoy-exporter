// logging.go - Enhanced logging configuration for systemd compatibility
package main

import (
	"log"
	"os"
	"strings"
)

// LogConfig holds logging configuration
type LogConfig struct {
	DisableTimestamp bool
	Prefix           string
}

// InitializeLogging configures logging based on the runtime environment
func InitializeLogging() {
	config := detectLoggingConfig()
	
	flags := log.LstdFlags
	if config.DisableTimestamp {
		flags = 0 // Remove all default flags including timestamp
	}
	
	if config.Prefix != "" {
		log.SetFlags(flags)
		log.SetPrefix(config.Prefix + " ")
	} else {
		log.SetFlags(flags)
	}
}

// detectLoggingConfig detects the appropriate logging configuration
func detectLoggingConfig() LogConfig {
	config := LogConfig{
		DisableTimestamp: false,
		Prefix:           "",
	}
	
	// Check if we're running under systemd
	if isRunningUnderSystemd() {
		config.DisableTimestamp = true
		// Optionally set a prefix for systemd logs
		config.Prefix = "[envoy-exporter]"
	}
	
	// Check for explicit environment variable override
	if os.Getenv("DISABLE_LOG_TIMESTAMP") == "true" || os.Getenv("DISABLE_LOG_TIMESTAMP") == "1" {
		config.DisableTimestamp = true
	}
	
	if prefix := os.Getenv("LOG_PREFIX"); prefix != "" {
		config.Prefix = prefix
	}
	
	return config
}

// isRunningUnderSystemd checks if the process is running under systemd
func isRunningUnderSystemd() bool {
	// Method 1: Check for systemd-specific environment variables
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	
	// Method 2: Check if parent process is systemd
	if ppid := os.Getppid(); ppid == 1 {
		// Additional check: see if init is systemd
		if cmdline, err := os.ReadFile("/proc/1/cmdline"); err == nil {
			if strings.Contains(string(cmdline), "systemd") {
				return true
			}
		}
	}
	
	// Method 3: Check for systemd journal socket
	if _, err := os.Stat("/run/systemd/journal/socket"); err == nil {
		// Also check if we're connected to journal
		if os.Getenv("JOURNAL_STREAM") != "" {
			return true
		}
	}
	
	// Method 4: Check if stderr is connected to systemd journal
	// This is a more reliable method for detecting journal output
	if journalStream := os.Getenv("JOURNAL_STREAM"); journalStream != "" {
		return true
	}
	
	return false
}

// LogInfo logs an info message (wrapper for consistent formatting)
func LogInfo(format string, v ...interface{}) {
	log.Printf(format, v...)
}

// LogError logs an error message (wrapper for consistent formatting)
func LogError(format string, v ...interface{}) {
	log.Printf("ERROR: "+format, v...)
}

// LogWarning logs a warning message (wrapper for consistent formatting)
func LogWarning(format string, v ...interface{}) {
	log.Printf("WARNING: "+format, v...)
}

// LogDebug logs a debug message (only if debug is enabled)
func LogDebug(format string, v ...interface{}) {
	if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1" {
		log.Printf("DEBUG: "+format, v...)
	}
}

