// version.go - Build and version information
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Build information variables (set by ldflags during build)
var (
	Version     = "dev"
	GitCommit   = "unknown"
	GitBranch   = "unknown"
	BuildTime   = "unknown"
	BuildUser   = "unknown"
	BuildHost   = "unknown"
	GoVersion   = "unknown"
	Platform    = "unknown"
	ModuleName  = "envoy-prometheus-exporter"
	GoModVersion = "unknown"
)

// BuildInfo contains all build and version information
type BuildInfo struct {
	Version       string    `json:"version"`
	GitCommit     string    `json:"git_commit"`
	GitBranch     string    `json:"git_branch"`
	BuildTime     string    `json:"build_time"`
	BuildTimeUTC  time.Time `json:"build_time_utc"`
	BuildUser     string    `json:"build_user"`
	BuildHost     string    `json:"build_host"`
	GoVersion     string    `json:"go_version"`
	GoRuntime     string    `json:"go_runtime"`
	Platform      string    `json:"platform"`
	Architecture  string    `json:"architecture"`
	ModuleName    string    `json:"module_name"`
	GoModVersion  string    `json:"go_mod_version"`
	Uptime        string    `json:"uptime"`
	StartTime     time.Time `json:"start_time"`
}

var (
	startTime = time.Now()
)

// GetBuildInfo returns comprehensive build information
func GetBuildInfo() *BuildInfo {
	buildTimeUTC, _ := time.Parse(time.RFC3339, BuildTime)
	
	// Parse Go version to get just the version number
	goVer := GoVersion
	if strings.HasPrefix(goVer, "go version ") {
		parts := strings.Fields(goVer)
		if len(parts) >= 3 {
			goVer = parts[2]
		}
	}
	
	return &BuildInfo{
		Version:       Version,
		GitCommit:     GitCommit,
		GitBranch:     GitBranch,
		BuildTime:     BuildTime,
		BuildTimeUTC:  buildTimeUTC,
		BuildUser:     BuildUser,
		BuildHost:     BuildHost,
		GoVersion:     goVer,
		GoRuntime:     runtime.Version(),
		Platform:      Platform,
		Architecture:  runtime.GOARCH,
		ModuleName:    ModuleName,
		GoModVersion:  GoModVersion,
		Uptime:        time.Since(startTime).String(),
		StartTime:     startTime,
	}
}

// GetVersionString returns a formatted version string
func GetVersionString() string {
	info := GetBuildInfo()
	return fmt.Sprintf("%s (%s) built on %s", info.Version, info.GitCommit, info.BuildTime)
}

// GetDetailedVersionString returns a detailed multi-line version string
func GetDetailedVersionString() string {
	info := GetBuildInfo()
	
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Version:        %s\n", info.Version))
	sb.WriteString(fmt.Sprintf("Git Commit:     %s\n", info.GitCommit))
	sb.WriteString(fmt.Sprintf("Git Branch:     %s\n", info.GitBranch))
	sb.WriteString(fmt.Sprintf("Build Time:     %s\n", info.BuildTime))
	sb.WriteString(fmt.Sprintf("Build User:     %s@%s\n", info.BuildUser, info.BuildHost))
	sb.WriteString(fmt.Sprintf("Go Version:     %s\n", info.GoVersion))
	sb.WriteString(fmt.Sprintf("Go Runtime:     %s\n", info.GoRuntime))
	sb.WriteString(fmt.Sprintf("Platform:       %s/%s\n", runtime.GOOS, info.Architecture))
	sb.WriteString(fmt.Sprintf("Module:         %s\n", info.ModuleName))
	sb.WriteString(fmt.Sprintf("Uptime:         %s\n", info.Uptime))
	
	return sb.String()
}

// PrintVersionInfo prints version information to stdout
func PrintVersionInfo() {
	fmt.Print(GetDetailedVersionString())
}

// ServeVersionAPI serves version information as JSON API
func (e *EnvoyExporter) serveVersionAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	
	buildInfo := GetBuildInfo()
	
	// Add runtime information
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	response := map[string]interface{}{
		"build_info": buildInfo,
		"runtime_info": map[string]interface{}{
			"goroutines":     runtime.NumGoroutine(),
			"memory_alloc":   memStats.Alloc,
			"memory_sys":     memStats.Sys,
			"gc_runs":        memStats.NumGC,
			"next_gc":        memStats.NextGC,
			"cpu_count":      runtime.NumCPU(),
		},
		"config_info": map[string]interface{}{
			"envoy_ip":         e.config.EnvoyIP,
			"port":            e.config.Port,
			"web_dir":         e.config.WebDir,
			"queries":         len(e.config.Queries),
			"calculated_metrics": len(e.config.CalculatedMetrics.Metrics),
			"mqtt_enabled":    e.config.MQTT.Enabled,
			"production_tracking": e.productionTracker != nil,
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// Add version metrics to Prometheus metrics
func (e *EnvoyExporter) addVersionMetrics(metrics *strings.Builder) {
	info := GetBuildInfo()
	
	// Version info metric
	metrics.WriteString("# HELP envoy_exporter_build_info Build information\n")
	metrics.WriteString("# TYPE envoy_exporter_build_info gauge\n")
	metrics.WriteString(fmt.Sprintf(`envoy_exporter_build_info{version="%s",git_commit="%s",git_branch="%s",go_version="%s",platform="%s"} 1`+"\n",
		info.Version, info.GitCommit, info.GitBranch, info.GoVersion, info.Platform))
	
	// Start time metric
	metrics.WriteString("# HELP envoy_exporter_start_time_seconds Start time of the exporter\n")
	metrics.WriteString("# TYPE envoy_exporter_start_time_seconds gauge\n")
	metrics.WriteString(fmt.Sprintf("envoy_exporter_start_time_seconds %d\n", info.StartTime.Unix()))
	
	// Uptime metric
	metrics.WriteString("# HELP envoy_exporter_uptime_seconds Uptime of the exporter\n")
	metrics.WriteString("# TYPE envoy_exporter_uptime_seconds counter\n")
	metrics.WriteString(fmt.Sprintf("envoy_exporter_uptime_seconds %d\n", int64(time.Since(startTime).Seconds())))
}
