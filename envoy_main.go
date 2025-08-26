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
	// Initialize logging before anything else
	InitializeLogging()
	
	// Command line flags
	var (
		configFile = flag.String("config", "envoy_config.xml", "Configuration file path")
		version    = flag.Bool("version", false, "Show version information")
		versionF   = flag.Bool("v", false, "Show version information (short)")
		noTimestamp = flag.Bool("no-timestamp", false, "Disable log timestamps (useful for systemd)")
	)
	flag.Parse()

	// Handle explicit no-timestamp flag
	if *noTimestamp {
		log.SetFlags(0)
		log.SetPrefix("[envoy-exporter] ")
	}

	// Handle version flag
	if *version || *versionF {
		PrintVersionInfo()
		return
	}

	// Use provided config file or default
	if flag.NArg() > 0 {
		*configFile = flag.Arg(0)
	}

	LogInfo("Starting %s", GetVersionString())
	LogInfo("Using configuration file: %s", *configFile)

	exporter, err := NewEnvoyExporter(*configFile)
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		sig := <-sigChan
		LogInfo("Received signal %v, shutting down gracefully...", sig)
		
		// Shutdown production tracker
		if exporter.productionTracker != nil {
			exporter.productionTracker.Shutdown()
		}
		
		// Shutdown MQTT publisher
		if exporter.mqttPublisher != nil {
			exporter.mqttPublisher.Shutdown()
		}
		
		LogInfo("Graceful shutdown complete")
		os.Exit(0)
	}()

	// Ensure web directory exists
	if _, err := os.Stat(exporter.config.WebDir); os.IsNotExist(err) {
		LogInfo("Creating web directory: %s", exporter.config.WebDir)
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
	LogInfo("Enhanced Configuration-Driven Envoy Prometheus Exporter")
	LogInfo("Version: %s (%s)", buildInfo.Version, buildInfo.GitCommit)
	LogInfo("Built: %s by %s@%s", buildInfo.BuildTime, buildInfo.BuildUser, buildInfo.BuildHost)
	LogInfo("Go: %s (%s)", buildInfo.GoRuntime, buildInfo.Platform)
	LogInfo("Listening on: %s", listenAddr)
	LogInfo("Envoy IP: %s", exporter.config.EnvoyIP)
	LogInfo("Web Directory: %s", exporter.config.WebDir)
	LogInfo("Location: %.6f, %.6f", exporter.config.Latitude, exporter.config.Longitude)
	LogInfo("Production tracking enabled")
	
	// Log MQTT status
	if exporter.config.MQTT.Enabled {
		LogInfo("MQTT publishing enabled - Broker: %s:%d, Topic: %s, Interval: %ds", 
			exporter.config.MQTT.Broker, exporter.config.MQTT.Port, 
			exporter.config.MQTT.TopicPrefix, exporter.config.MQTT.PublishInterval)
	} else {
		LogInfo("MQTT publishing disabled")
	}
	log.Printf("Access the web interface at: http://localhost%s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
