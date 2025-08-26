package main

import (
	"flag"
	"log"
   "fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Command line flags
	var (
		configFile = flag.String("config", "envoy_config.xml", "Configuration file path")
		version    = flag.Bool("version", false, "Show version information")
		versionF   = flag.Bool("v", false, "Show version information (short)")
	)
	flag.Parse()

	// Handle version flag
	if *version || *versionF {
		PrintVersionInfo()
		return
	}

	// Use provided config file or default
	if flag.NArg() > 0 {
		*configFile = flag.Arg(0)
	}

	log.Printf("Starting %s", GetVersionString())
	log.Printf("Using configuration file: %s", *configFile)

	exporter, err := NewEnvoyExporter(*configFile)
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)
		
		// Shutdown production tracker
		if exporter.productionTracker != nil {
			exporter.productionTracker.Shutdown()
		}
		
		// Shutdown MQTT publisher
		if exporter.mqttPublisher != nil {
			exporter.mqttPublisher.Shutdown()
		}
		
		log.Printf("Graceful shutdown complete")
		os.Exit(0)
	}()

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
	http.HandleFunc("/api/daily-production", exporter.serveDailyProductionAPI)
	http.HandleFunc("/api/mqtt-status", exporter.serveMQTTStatusAPI)
	http.HandleFunc("/api/version", exporter.serveVersionAPI)
	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, GetDetailedVersionString())
	})
	http.Handle("/", exporter.serveStaticFiles())

	listenAddr := ":" + exporter.config.Port
	
	// Log startup information
	buildInfo := GetBuildInfo()
	log.Printf("Enhanced Configuration-Driven Envoy Prometheus Exporter")
	log.Printf("Version: %s (%s)", buildInfo.Version, buildInfo.GitCommit)
	log.Printf("Built: %s by %s@%s", buildInfo.BuildTime, buildInfo.BuildUser, buildInfo.BuildHost)
	log.Printf("Go: %s (%s)", buildInfo.GoRuntime, buildInfo.Platform)
	log.Printf("Listening on: %s", listenAddr)
	log.Printf("Envoy IP: %s", exporter.config.EnvoyIP)
	log.Printf("Web Directory: %s", exporter.config.WebDir)
	log.Printf("Location: %.6f, %.6f", exporter.config.Latitude, exporter.config.Longitude)
	log.Printf("Production tracking enabled")
	
	// Log MQTT status
	if exporter.config.MQTT.Enabled {
		log.Printf("MQTT publishing enabled - Broker: %s:%d, Topic: %s, Interval: %ds", 
			exporter.config.MQTT.Broker, exporter.config.MQTT.Port, 
			exporter.config.MQTT.TopicPrefix, exporter.config.MQTT.PublishInterval)
	} else {
		log.Printf("MQTT publishing disabled")
	}
	
	log.Printf("Access the web interface at: http://localhost%s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
