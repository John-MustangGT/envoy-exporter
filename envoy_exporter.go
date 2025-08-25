package main

import (
	"crypto/tls"
	"encoding/json"
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

	// Set default MQTT port if not specified
	if config.MQTT.Enabled && config.MQTT.Port == 0 {
		if config.MQTT.TLS {
			config.MQTT.Port = 8883
		} else {
			config.MQTT.Port = 1883
		}
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

	// Initialize production tracking
	exporter.initProductionTracking()

	// Initialize MQTT publisher
	exporter.initMQTTPublisher()

	return exporter, nil
}

// MQTT Status API endpoint
func (e *EnvoyExporter) serveMQTTStatusAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	status := map[string]interface{}{
		"enabled":   e.config.MQTT.Enabled,
		"connected": false,
		"broker":    "",
		"topic_prefix": "",
		"publish_interval": 0,
		"last_publish": 0,
	}

	if e.config.MQTT.Enabled {
		status["broker"] = fmt.Sprintf("%s:%d", e.config.MQTT.Broker, e.config.MQTT.Port)
		status["topic_prefix"] = e.config.MQTT.TopicPrefix
		status["publish_interval"] = e.config.MQTT.PublishInterval
		
		if e.mqttPublisher != nil {
			status["connected"] = e.mqttPublisher.IsConnected()
			status["last_publish"] = e.mqttPublisher.lastPublish
		}
	}

	json.NewEncoder(w).Encode(status)
}
