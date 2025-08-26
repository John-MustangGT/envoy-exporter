package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func (e *EnvoyExporter) createDefaultWebFiles() {
	// Create index.html (keep existing code)
	indexHTML := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Envoy Prometheus Exporter</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background: #f5f5f5; }
        .container { max-width: 1200px; margin: 0 auto; background: white; padding: 30px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #2c3e50; text-align: center; }
        .endpoints { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px; margin: 30px 0; }
        .endpoint-card { background: #ecf0f1; padding: 20px; border-radius: 6px; text-align: center; }
        .endpoint-card h3 { margin-top: 0; color: #34495e; }
        .endpoint-card a { display: inline-block; background: #3498db; color: white; padding: 10px 20px; text-decoration: none; border-radius: 4px; margin: 5px; }
        .endpoint-card a:hover { background: #2980b9; }
        .monitor-link { background: #e74c3c !important; }
        .monitor-link:hover { background: #c0392b !important; }
        .info-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 20px; margin: 30px 0; }
        .info-card { background: #f8f9fa; padding: 15px; border-radius: 6px; }
        .info-card h4 { margin-top: 0; color: #2c3e50; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Enhanced Envoy Prometheus Exporter</h1>
        
        <div class="endpoints">
            <div class="endpoint-card">
                <h3>📊 Live Monitor</h3>
                <p>Real-time solar production dashboard with live data, solar position, and daily production graphs</p>
                <a href="/monitor.html" class="monitor-link">Open Monitor</a>
            </div>
            <div class="endpoint-card">
                <h3>📈 Prometheus Metrics</h3>
                <p>Raw metrics endpoint for Prometheus scraping</p>
                <a href="/metrics">View Metrics</a>
            </div>
            <div class="endpoint-card">
                <h3>❤️ Health Check</h3>
                <p>System health and query success rates</p>
                <a href="/health">Check Health</a>
            </div>
            <div class="endpoint-card">
                <h3>🔧 Debug Info</h3>
                <p>Detailed diagnostics and troubleshooting</p>
                <a href="/debug">Debug Info</a>
            </div>
        </div>

        <div class="info-grid">
            <div class="info-card">
                <h4>System Information</h4>
                <p><strong>Envoy IP:</strong> ` + e.config.EnvoyIP + `</p>
                <p><strong>Port:</strong> ` + e.config.Port + `</p>
                <p><strong>Location:</strong> ` + fmt.Sprintf("%.6f, %.6f", e.config.Latitude, e.config.Longitude) + `</p>
            </div>
            <div class="info-card">
                <h4>Configuration</h4>
                <p><strong>Queries:</strong> ` + strconv.Itoa(len(e.config.Queries)) + `</p>
                <p><strong>Total Metrics:</strong> ` + strconv.Itoa(e.getTotalMetricCount()) + `</p>
                <p><strong>Calculated Metrics:</strong> ` + strconv.Itoa(len(e.config.CalculatedMetrics.Metrics)) + `</p>
                <p><strong>Production Tracking:</strong> Enabled</p>
            </div>
        </div>

        <div style="margin-top: 40px; padding-top: 20px; border-top: 1px solid #ecf0f1; text-align: center; color: #7f8c8d;">
            <p>Enhanced Configuration-Driven Envoy Prometheus Exporter</p>
            <p>Real-time monitoring • Solar position tracking • Daily production graphs • Comprehensive diagnostics</p>
        </div>
    </div>
</body>
</html>`

	// Use the new enhanced monitor.html from the artifact
	monitorHTML := getEnhancedMonitorHTML() // This would contain the full HTML from the artifact

	// Write the files
	os.WriteFile(filepath.Join(e.config.WebDir, "index.html"), []byte(indexHTML), 0644)
	os.WriteFile(filepath.Join(e.config.WebDir, "monitor.html"), []byte(monitorHTML), 0644)
	
	LogInfo("Created default web files with production tracking in %s", e.config.WebDir)
}

// Helper function to get the enhanced monitor HTML
func getEnhancedMonitorHTML() string {
	// This would contain the full HTML content from the enhanced_monitor_page artifact
	// For brevity, I'm indicating where you would place the content
	return `<!-- The full enhanced monitor.html content goes here -->`
}

