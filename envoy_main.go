package main

import (
	"log"
	"net/http"
	"os"
)
// Include all the existing methods (checkCondition, transformValue, processMetric, etc.)
// For brevity, I'll include the key new web-related methods and main function

func main() {
	configFile := "envoy_config.xml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	exporter, err := NewEnvoyExporter(configFile)
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	// Ensure web directory exists
	if _, err := os.Stat(exporter.config.WebDir); os.IsNotExist(err) {
		log.Printf("Creating web directory: %s", exporter.config.WebDir)
		os.MkdirAll(exporter.config.WebDir, 0755)
		
		// Create default files if they don't exist
		exporter.createDefaultWebFiles()
	}

	// Set up HTTP routes
	http.HandleFunc("/metrics", exporter.serveMetrics)
	http.HandleFunc("/health", exporter.serveHealth)
	http.HandleFunc("/debug", exporter.serveDebug)
	http.HandleFunc("/api/monitor", exporter.serveMonitorAPI)
	http.HandleFunc("/api/daily-production", exporter.serveDailyProductionAPI) // ADD THIS LINE
	http.Handle("/", exporter.serveStaticFiles())

	listenAddr := ":" + exporter.config.Port
	log.Printf("Starting Enhanced Configuration-Driven Envoy Prometheus Exporter on %s", listenAddr)
	log.Printf("Envoy IP: %s", exporter.config.EnvoyIP)
	log.Printf("Web Directory: %s", exporter.config.WebDir)
	log.Printf("Location: %.6f, %.6f", exporter.config.Latitude, exporter.config.Longitude)
	log.Printf("Production tracking enabled")  // ADD THIS LINE
	
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
