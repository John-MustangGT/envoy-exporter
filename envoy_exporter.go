package main

import (
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"time"
)

func NewEnvoyExporter(configFile string) (*EnvoyExporter, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	err = xml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config XML: %w", err)
	}

	// Set default web directory if not specified
	if config.WebDir == "" {
		config.WebDir = "./web"
	}

	// Create HTTP client with insecure TLS (for self-signed certs)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	exporter := &EnvoyExporter{
		config:       config,
		httpClient:   client,
		metricCache:  make(map[string]float64),
		queryResults: make(map[string]QueryResult),
	}

	// Get initial token
	err = exporter.refreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial token: %w", err)
	}

	// Start token refresh goroutine
	go exporter.tokenRefreshLoop()

	// Start monitor data refresh goroutine
	go exporter.monitorDataRefreshLoop()

	// Initialize production tracking - ADD THIS
	exporter.initProductionTracking()

	return exporter, nil
}

