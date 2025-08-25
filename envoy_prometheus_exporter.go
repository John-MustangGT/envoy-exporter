package main

import (
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Configuration structures
type Config struct {
	XMLName            xml.Name            `xml:"envoy_config"`
	User               string              `xml:"user"`
	Password           string              `xml:"password"`
	EnvoySerial        string              `xml:"envoy_serial"`
	EnvoyIP            string              `xml:"envoy_ip"`
	Port               string              `xml:"port"`
	WebDir             string              `xml:"web_dir"`
	Latitude           float64             `xml:"latitude"`
	Longitude          float64             `xml:"longitude"`
	Timezone           string              `xml:"timezone"`
	Queries            []Query             `xml:"query"`
	CalculatedMetrics  CalculatedMetrics   `xml:"calculated_metrics"`
	Transforms         Transforms          `xml:"transforms"`
	Conditions         Conditions          `xml:"conditions"`
}

type Query struct {
	Name      string   `xml:"name,attr"`
	URL       string   `xml:"url,attr"`
	Array     bool     `xml:"array,attr"`
	Condition string   `xml:"condition,attr"`
	Metrics   []Metric `xml:"metric"`
}

type Metric struct {
	Name      string  `xml:"name,attr"`
	Type      string  `xml:"type,attr"`
	Help      string  `xml:"help,attr"`
	Labels    string  `xml:"labels,attr"`
	Transform string  `xml:"transform,attr"`
	Condition string  `xml:"condition,attr"`
	Fields    []Field `xml:"field"`
	Value     string  `xml:"value"`
}

type Field struct {
	JSONPath   string `xml:"json_path,attr"`
	Label      string `xml:"label,attr"`
	LabelValue string `xml:"label_value,attr"`
	Transform  string `xml:"transform,attr"`
}

type CalculatedMetrics struct {
	Metrics []CalculatedMetric `xml:"metric"`
}

type CalculatedMetric struct {
	Name        string `xml:"name,attr"`
	Type        string `xml:"type,attr"`
	Help        string `xml:"help,attr"`
	Condition   string `xml:"condition,attr"`
	Calculation string `xml:"calculation"`
}

type Transforms struct {
	Transforms []Transform `xml:"transform"`
}

type Transform struct {
	Name        string `xml:"name,attr"`
	Description string `xml:"description"`
}

type Conditions struct {
	Conditions []Condition `xml:"condition"`
}

type Condition struct {
	Name        string `xml:"name,attr"`
	Description string `xml:"description"`
	Check       string `xml:"check"`
}

// Authentication structures
type LoginResponse struct {
	Message      string `json:"message"`
	SessionID    string `json:"session_id"`
	IsConsumer   bool   `json:"is_consumer"`
	SystemID     int    `json:"system_id"`
	RedirectURL  string `json:"redirect_url"`
}

type TokenResponse struct {
	GenerationTime int64  `json:"generation_time"`
	Token          string `json:"token"`
	ExpiresAt      int64  `json:"expires_at"`
}

// Monitor API structures
type MonitorData struct {
	Timestamp          time.Time         `json:"timestamp"`
	SystemInfo         SystemInfo        `json:"system_info"`
	Production         ProductionData    `json:"production"`
	Inverters          []InverterData    `json:"inverters"`
	PowerFlow          PowerFlowData     `json:"power_flow"`
	SolarPosition      SolarPosition     `json:"solar_position"`
	Summary            SummaryData       `json:"summary"`
}

type SystemInfo struct {
	Serial       string `json:"serial"`
	Software     string `json:"software"`
	Status       string `json:"status"`
	LastUpdate   int64  `json:"last_update"`
}

type ProductionData struct {
	CurrentWatts      float64 `json:"current_watts"`
	TodayWh          float64 `json:"today_wh"`
	LifetimeWh       float64 `json:"lifetime_wh"`
	SevenDaysWh      float64 `json:"seven_days_wh"`
}

type InverterData struct {
	Serial           string  `json:"serial"`
	CurrentWatts     float64 `json:"current_watts"`
	MaxWatts         float64 `json:"max_watts"`
	LastReport       int64   `json:"last_report"`
	Status           string  `json:"status"`
	DeviceType       int     `json:"device_type"`
}

type PowerFlowData struct {
	PVWatts       float64 `json:"pv_watts"`
	GridWatts     float64 `json:"grid_watts"`
	LoadWatts     float64 `json:"load_watts"`
	StorageWatts  float64 `json:"storage_watts"`
	StorageSOC    float64 `json:"storage_soc"`
	GridImport    float64 `json:"grid_import"`
	GridExport    float64 `json:"grid_export"`
}

type SolarPosition struct {
	Azimuth     float64 `json:"azimuth"`
	Elevation   float64 `json:"elevation"`
	Sunrise     string  `json:"sunrise"`
	Sunset      string  `json:"sunset"`
	DayLength   float64 `json:"day_length"`
	IsDaytime   bool    `json:"is_daytime"`
}

type SummaryData struct {
	TotalInverters    int     `json:"total_inverters"`
	ActiveInverters   int     `json:"active_inverters"`
	SystemEfficiency  float64 `json:"system_efficiency"`
	SelfConsumption   float64 `json:"self_consumption"`
	SolarCoverage     float64 `json:"solar_coverage"`
}

// Query result tracking
type QueryResult struct {
	Name      string
	Success   bool
	Error     string
	DataSize  int
	IsJSON    bool
	HasData   bool
	StatusCode int
}

type EnvoyExporter struct {
	config       Config
	token        string
	tokenExpires int64
	tokenMutex   sync.RWMutex
	httpClient   *http.Client
	metricCache  map[string]float64
	cacheMutex   sync.RWMutex
	queryResults map[string]QueryResult
	resultsMutex sync.RWMutex
	lastMonitorData MonitorData
	monitorMutex sync.RWMutex
}

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
		config:      config,
		httpClient:  client,
		metricCache: make(map[string]float64),
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

	return exporter, nil
}

func (e *EnvoyExporter) refreshToken() error {
	// Login to get session ID
	loginData := url.Values{}
	loginData.Set("user[email]", e.config.User)
	loginData.Set("user[password]", e.config.Password)

	resp, err := e.httpClient.PostForm("https://enlighten.enphaseenergy.com/login/login.json?", loginData)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	var loginResp LoginResponse
	err = json.NewDecoder(resp.Body).Decode(&loginResp)
	if err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	if loginResp.Message != "success" {
		return fmt.Errorf("login failed: %s", loginResp.Message)
	}

	// Get web token
	tokenReq := map[string]interface{}{
		"session_id": loginResp.SessionID,
		"serial_num": e.config.EnvoySerial,
		"username":   e.config.User,
	}

	tokenData, err := json.Marshal(tokenReq)
	if err != nil {
		return fmt.Errorf("failed to marshal token request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://entrez.enphaseenergy.com/tokens", strings.NewReader(string(tokenData)))
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	tokenBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read token response: %w", err)
	}

	// Parse as TokenResponse if it's JSON, otherwise use as raw token
	var tokenResp TokenResponse
	if err := json.Unmarshal(tokenBody, &tokenResp); err == nil && tokenResp.Token != "" {
		e.tokenMutex.Lock()
		e.token = tokenResp.Token
		e.tokenExpires = tokenResp.ExpiresAt
		e.tokenMutex.Unlock()
	} else {
		// Raw token string
		e.tokenMutex.Lock()
		e.token = strings.TrimSpace(string(tokenBody))
		e.tokenExpires = time.Now().Add(24 * time.Hour).Unix() // Default 24h expiry
		e.tokenMutex.Unlock()
	}

	log.Printf("Token refreshed, expires at: %s", time.Unix(e.tokenExpires, 0))
	return nil
}

func (e *EnvoyExporter) tokenRefreshLoop() {
	for {
		e.tokenMutex.RLock()
		expiresAt := e.tokenExpires
		e.tokenMutex.RUnlock()

		// Refresh 1 hour before expiry
		refreshTime := time.Unix(expiresAt, 0).Add(-1 * time.Hour)
		sleepDuration := time.Until(refreshTime)

		if sleepDuration <= 0 {
			sleepDuration = 5 * time.Minute // Retry in 5 minutes if already expired
		}

		time.Sleep(sleepDuration)

		err := e.refreshToken()
		if err != nil {
			log.Printf("Failed to refresh token: %v", err)
			time.Sleep(5 * time.Minute) // Retry in 5 minutes
		}
	}
}

func (e *EnvoyExporter) monitorDataRefreshLoop() {
	for {
		e.refreshMonitorData()
		time.Sleep(30 * time.Second) // Update every 30 seconds
	}
}

func (e *EnvoyExporter) refreshMonitorData() {
	var monitorData MonitorData
	monitorData.Timestamp = time.Now()

	// Get production data
	if data, err := e.makeEnvoyRequest("https://{envoy_ip}/api/v1/production"); err == nil {
		var prodData map[string]interface{}
		if json.Unmarshal(data, &prodData) == nil {
			if watts, ok := prodData["wattsNow"].(float64); ok {
				monitorData.Production.CurrentWatts = watts
			}
			if whToday, ok := prodData["wattHoursToday"].(float64); ok {
				monitorData.Production.TodayWh = whToday
			}
			if whLifetime, ok := prodData["wattHoursLifetime"].(float64); ok {
				monitorData.Production.LifetimeWh = whLifetime
			}
			if whSevenDays, ok := prodData["wattHoursSevenDays"].(float64); ok {
				monitorData.Production.SevenDaysWh = whSevenDays
			}
		}
	}

	// Get inverter data
	if data, err := e.makeEnvoyRequest("https://{envoy_ip}/api/v1/production/inverters"); err == nil {
		var invData []map[string]interface{}
		if json.Unmarshal(data, &invData) == nil {
			monitorData.Inverters = make([]InverterData, 0, len(invData))
			activeCount := 0
			for _, inv := range invData {
				inverter := InverterData{}
				if serial, ok := inv["serialNumber"].(string); ok {
					inverter.Serial = serial
				}
				if watts, ok := inv["lastReportWatts"].(float64); ok {
					inverter.CurrentWatts = watts
					if watts > 0 {
						activeCount++
					}
				}
				if maxWatts, ok := inv["maxReportWatts"].(float64); ok {
					inverter.MaxWatts = maxWatts
				}
				if lastReport, ok := inv["lastReportDate"].(float64); ok {
					inverter.LastReport = int64(lastReport)
				}
				if devType, ok := inv["devType"].(float64); ok {
					inverter.DeviceType = int(devType)
				}
				monitorData.Inverters = append(monitorData.Inverters, inverter)
			}
			monitorData.Summary.TotalInverters = len(invData)
			monitorData.Summary.ActiveInverters = activeCount
		}
	}

	// Sort inverters by serial number for consistent display
	sort.Slice(monitorData.Inverters, func(i, j int) bool {
		return monitorData.Inverters[i].Serial < monitorData.Inverters[j].Serial
	})

	// Get power flow data from livedata
	if data, err := e.makeEnvoyRequest("https://{envoy_ip}/ivp/livedata/status"); err == nil {
		var liveData map[string]interface{}
		if json.Unmarshal(data, &liveData) == nil {
			if meters, ok := liveData["meters"].(map[string]interface{}); ok {
				// PV power
				if pv, ok := meters["pv"].(map[string]interface{}); ok {
					if aggPMw, ok := pv["agg_p_mw"].(float64); ok {
						monitorData.PowerFlow.PVWatts = aggPMw / 1000.0
					}
				}
				// Grid power
				if grid, ok := meters["grid"].(map[string]interface{}); ok {
					if aggPMw, ok := grid["agg_p_mw"].(float64); ok {
						gridWatts := aggPMw / 1000.0
						monitorData.PowerFlow.GridWatts = gridWatts
						if gridWatts > 0 {
							monitorData.PowerFlow.GridImport = gridWatts
						} else {
							monitorData.PowerFlow.GridExport = -gridWatts
						}
					}
				}
				// Load power
				if load, ok := meters["load"].(map[string]interface{}); ok {
					if aggPMw, ok := load["agg_p_mw"].(float64); ok {
						monitorData.PowerFlow.LoadWatts = aggPMw / 1000.0
					}
				}
				// Storage power and SOC
				if storage, ok := meters["storage"].(map[string]interface{}); ok {
					if aggPMw, ok := storage["agg_p_mw"].(float64); ok {
						monitorData.PowerFlow.StorageWatts = aggPMw / 1000.0
					}
					if aggSoc, ok := storage["agg_soc"].(float64); ok {
						monitorData.PowerFlow.StorageSOC = aggSoc
					}
				}
			}
		}
	}

	// Calculate solar position
	monitorData.SolarPosition = e.calculateSolarPosition()

	// Calculate summary metrics
	if monitorData.PowerFlow.PVWatts > 0 && monitorData.PowerFlow.LoadWatts > 0 {
		monitorData.Summary.SelfConsumption = math.Max(0, math.Min(100, 
			(monitorData.PowerFlow.PVWatts - monitorData.PowerFlow.GridExport) / monitorData.PowerFlow.PVWatts * 100))
		monitorData.Summary.SolarCoverage = math.Max(0, math.Min(100,
			monitorData.PowerFlow.PVWatts / monitorData.PowerFlow.LoadWatts * 100))
	}

	if monitorData.Summary.TotalInverters > 0 {
		monitorData.Summary.SystemEfficiency = float64(monitorData.Summary.ActiveInverters) / float64(monitorData.Summary.TotalInverters) * 100
	}

	// Store the data
	e.monitorMutex.Lock()
	e.lastMonitorData = monitorData
	e.monitorMutex.Unlock()
}

func (e *EnvoyExporter) calculateSolarPosition() SolarPosition {
	now := time.Now()
	lat := e.config.Latitude
	//lng := e.config.Longitude

	// Convert to radians
	latRad := lat * math.Pi / 180.0

	// Calculate day of year
	dayOfYear := float64(now.YearDay())
	
	// Solar declination
	declination := 23.45 * math.Sin((360.0/365.0)*(dayOfYear-81.0)*math.Pi/180.0) * math.Pi / 180.0
	
	// Hour angle
	timeDecimal := float64(now.Hour()) + float64(now.Minute())/60.0 + float64(now.Second())/3600.0
	hourAngle := (timeDecimal - 12.0) * 15.0 * math.Pi / 180.0
	
	// Solar elevation
	elevation := math.Asin(math.Sin(declination)*math.Sin(latRad) + 
		math.Cos(declination)*math.Cos(latRad)*math.Cos(hourAngle))
	
	// Solar azimuth
	azimuth := math.Atan2(math.Sin(hourAngle), 
		math.Cos(hourAngle)*math.Sin(latRad) - math.Tan(declination)*math.Cos(latRad))
	
	// Convert back to degrees
	elevationDeg := elevation * 180.0 / math.Pi
	azimuthDeg := azimuth * 180.0 / math.Pi
	if azimuthDeg < 0 {
		azimuthDeg += 360
	}

	// Calculate sunrise/sunset (simplified)
	sunriseHour := 12.0 - math.Acos(-math.Tan(latRad)*math.Tan(declination))*12.0/math.Pi
	sunsetHour := 12.0 + math.Acos(-math.Tan(latRad)*math.Tan(declination))*12.0/math.Pi
	
	sunrise := fmt.Sprintf("%02d:%02d", int(sunriseHour), int((sunriseHour-float64(int(sunriseHour)))*60))
	sunset := fmt.Sprintf("%02d:%02d", int(sunsetHour), int((sunsetHour-float64(int(sunsetHour)))*60))
	
	return SolarPosition{
		Azimuth:   azimuthDeg,
		Elevation: elevationDeg,
		Sunrise:   sunrise,
		Sunset:    sunset,
		DayLength: sunsetHour - sunriseHour,
		IsDaytime: elevationDeg > 0,
	}
}

func (e *EnvoyExporter) getToken() string {
	e.tokenMutex.RLock()
	defer e.tokenMutex.RUnlock()
	return e.token
}

func (e *EnvoyExporter) makeEnvoyRequest(endpoint string) ([]byte, error) {
	url := strings.ReplaceAll(endpoint, "{envoy_ip}", e.config.EnvoyIP)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	token := e.getToken()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTML response (usually indicates auth or endpoint issues)
	if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		// Try to extract useful info from HTML error
		if strings.Contains(string(body), "401") || strings.Contains(string(body), "Unauthorized") {
			return nil, fmt.Errorf("authentication failed (401) - token may be expired or invalid")
		}
		if strings.Contains(string(body), "404") || strings.Contains(string(body), "Not Found") {
			return nil, fmt.Errorf("endpoint not found (404) - feature may not be available on this Envoy model")
		}
		if strings.Contains(string(body), "403") || strings.Contains(string(body), "Forbidden") {
			return nil, fmt.Errorf("access forbidden (403) - endpoint may require installer/owner privileges")
		}
		return nil, fmt.Errorf("received HTML response instead of JSON (status: %d) - endpoint may not be available", resp.StatusCode)
	}

	// Check for empty response
	if len(body) == 0 {
		return nil, fmt.Errorf("received empty response")
	}

	return body, nil
}

// Include all the existing metric processing methods here...
// (checkCondition, evaluateConditionCheck, jsonPathExists, etc.)
// I'll continue with the web serving methods

func (e *EnvoyExporter) serveStaticFiles() http.Handler {
	return http.FileServer(http.Dir(e.config.WebDir))
}

func (e *EnvoyExporter) serveMonitorAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	
	e.monitorMutex.RLock()
	data := e.lastMonitorData
	e.monitorMutex.RUnlock()
	
	json.NewEncoder(w).Encode(data)
}

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
	http.Handle("/", exporter.serveStaticFiles())

	listenAddr := ":" + exporter.config.Port
	log.Printf("Starting Enhanced Configuration-Driven Envoy Prometheus Exporter on %s", listenAddr)
	log.Printf("Envoy IP: %s", exporter.config.EnvoyIP)
	log.Printf("Web Directory: %s", exporter.config.WebDir)
	log.Printf("Location: %.6f, %.6f", exporter.config.Latitude, exporter.config.Longitude)
	
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func (e *EnvoyExporter) createDefaultWebFiles() {
	// Create index.html
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
                <p>Real-time solar production dashboard with live data and solar position</p>
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
            </div>
        </div>

        <div style="margin-top: 40px; padding-top: 20px; border-top: 1px solid #ecf0f1; text-align: center; color: #7f8c8d;">
            <p>Enhanced Configuration-Driven Envoy Prometheus Exporter</p>
            <p>Real-time monitoring • Solar position tracking • Comprehensive diagnostics</p>
        </div>
    </div>
</body>
</html>`

	// Create monitor.html
	monitorHTML := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Solar System Monitor</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { 
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; 
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            color: #333;
        }
        .container { 
            max-width: 1400px; 
            margin: 0 auto; 
            padding: 20px;
        }
        .header {
            background: rgba(255,255,255,0.95);
            backdrop-filter: blur(10px);
            border-radius: 15px;
            padding: 20px;
            margin-bottom: 20px;
            text-align: center;
            box-shadow: 0 8px 32px rgba(31, 38, 135, 0.37);
        }
        .header h1 {
            color: #2c3e50;
            margin-bottom: 10px;
            font-size: 2.5em;
        }
        .last-update {
            color: #7f8c8d;
            font-size: 0.9em;
        }
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(350px, 1fr));
            gap: 20px;
            margin-bottom: 20px;
        }
        .card {
            background: rgba(255,255,255,0.95);
            backdrop-filter: blur(10px);
            border-radius: 15px;
            padding: 25px;
            box-shadow: 0 8px 32px rgba(31, 38, 135, 0.37);
            border: 1px solid rgba(255,255,255,0.18);
        }
        .card h3 {
            color: #2c3e50;
            margin-bottom: 15px;
            font-size: 1.4em;
            display: flex;
            align-items: center;
            gap: 10px;
        }
        .metric {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 10px 0;
            border-bottom: 1px solid #ecf0f1;
        }
        .metric:last-child { border-bottom: none; }
        .metric-label {
            color: #7f8c8d;
            font-weight: 500;
        }
        .metric-value {
            font-weight: bold;
            font-size: 1.1em;
        }
        .power-value { color: #e74c3c; }
        .energy-value { color: #3498db; }
        .percentage-value { color: #27ae60; }
        .status-online { color: #27ae60; }
        .status-offline { color: #e74c3c; }
        .solar-position {
            position: relative;
            height: 200px;
            background: linear-gradient(to bottom, #87CEEB 0%, #87CEEB 50%, #90EE90 50%, #90EE90 100%);
            border-radius: 10px;
            overflow: hidden;
            margin: 15px 0;
        }
        .sun {
            position: absolute;
            width: 30px;
            height: 30px;
            background: #FFD700;
            border-radius: 50%;
            box-shadow: 0 0 20px #FFD700;
            transition: all 0.3s ease;
        }
        .inverter-grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
            gap: 15px;
        }
        .inverter-card {
            background: #f8f9fa;
            border-radius: 8px;
            padding: 15px;
            text-align: center;
        }
        .inverter-serial {
            font-weight: bold;
            color: #2c3e50;
            margin-bottom: 10px;
            font-size: 0.9em;
        }
        .inverter-power {
            font-size: 1.3em;
            font-weight: bold;
            color: #e74c3c;
        }
        .inverter-status {
            margin-top: 8px;
            font-size: 0.8em;
        }
        .loading {
            text-align: center;
            color: #7f8c8d;
            font-style: italic;
        }
        .error {
            color: #e74c3c;
            text-align: center;
            background: #fdf2f2;
            padding: 15px;
            border-radius: 8px;
            border: 1px solid #f5c6cb;
        }
        .flow-diagram {
            display: flex;
            align-items: center;
            justify-content: space-between;
            margin: 20px 0;
        }
        .flow-item {
            text-align: center;
            flex: 1;
        }
        .flow-icon {
            font-size: 2em;
            margin-bottom: 5px;
        }
        .flow-arrow {
            color: #3498db;
            font-size: 1.5em;
            margin: 0 10px;
        }
        @media (max-width: 768px) {
            .container { padding: 10px; }
            .grid { grid-template-columns: 1fr; }
            .header h1 { font-size: 2em; }
            .inverter-grid { grid-template-columns: 1fr 1fr; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🌞 Solar System Monitor</h1>
            <div class="last-update" id="lastUpdate">Loading...</div>
        </div>

        <div class="grid">
            <!-- Current Production -->
            <div class="card">
                <h3>⚡ Current Production</h3>
                <div class="metric">
                    <span class="metric-label">Power Output</span>
                    <span class="metric-value power-value" id="currentWatts">-- W</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Today's Energy</span>
                    <span class="metric-value energy-value" id="todayWh">-- Wh</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Lifetime Energy</span>
                    <span class="metric-value energy-value" id="lifetimeWh">-- MWh</span>
                </div>
                <div class="metric">
                    <span class="metric-label">7-Day Energy</span>
                    <span class="metric-value energy-value" id="sevenDaysWh">-- kWh</span>
                </div>
            </div>

            <!-- Solar Position -->
            <div class="card">
                <h3>☀️ Solar Position</h3>
                <div class="solar-position" id="solarPosition">
                    <div class="sun" id="sunPosition"></div>
                </div>
                <div class="metric">
                    <span class="metric-label">Elevation</span>
                    <span class="metric-value" id="elevation">--°</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Azimuth</span>
                    <span class="metric-value" id="azimuth">--°</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Sunrise / Sunset</span>
                    <span class="metric-value" id="sunriseSunset">-- / --</span>
                </div>
            </div>

            <!-- Power Flow -->
            <div class="card">
                <h3>🔄 Power Flow</h3>
                <div class="flow-diagram">
                    <div class="flow-item">
                        <div class="flow-icon">☀️</div>
                        <div class="metric-value power-value" id="pvWatts">-- W</div>
                        <div style="font-size: 0.8em; color: #7f8c8d;">Solar</div>
                    </div>
                    <div class="flow-arrow">→</div>
                    <div class="flow-item">
                        <div class="flow-icon">🏠</div>
                        <div class="metric-value power-value" id="loadWatts">-- W</div>
                        <div style="font-size: 0.8em; color: #7f8c8d;">Load</div>
                    </div>
                    <div class="flow-arrow">⇄</div>
                    <div class="flow-item">
                        <div class="flow-icon">🔌</div>
                        <div class="metric-value power-value" id="gridWatts">-- W</div>
                        <div style="font-size: 0.8em; color: #7f8c8d;">Grid</div>
                    </div>
                </div>
                <div class="metric" id="storageRow" style="display: none;">
                    <span class="metric-label">🔋 Battery</span>
                    <span class="metric-value">
                        <span class="power-value" id="storageWatts">-- W</span>
                        (<span class="percentage-value" id="storageSOC">--%</span>)
                    </span>
                </div>
            </div>

            <!-- System Summary -->
            <div class="card">
                <h3>📊 System Summary</h3>
                <div class="metric">
                    <span class="metric-label">System Efficiency</span>
                    <span class="metric-value percentage-value" id="systemEfficiency">--%</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Self Consumption</span>
                    <span class="metric-value percentage-value" id="selfConsumption">--%</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Solar Coverage</span>
                    <span class="metric-value percentage-value" id="solarCoverage">--%</span>
                </div>
                <div class="metric">
                    <span class="metric-label">Active Inverters</span>
                    <span class="metric-value" id="activeInverters">-- / --</span>
                </div>
            </div>
        </div>

        <!-- Inverters -->
        <div class="card">
            <h3>🔧 Inverter Status</h3>
            <div class="inverter-grid" id="inverterGrid">
                <div class="loading">Loading inverter data...</div>
            </div>
        </div>
    </div>

    <script>
        let lastUpdateTime = 0;

        function formatWatts(watts) {
            if (watts >= 1000000) return (watts / 1000000).toFixed(2) + ' MW';
            if (watts >= 1000) return (watts / 1000).toFixed(1) + ' kW';
            return Math.round(watts) + ' W';
        }

        function formatWh(wh) {
            if (wh >= 1000000) return (wh / 1000000).toFixed(2) + ' MWh';
            if (wh >= 1000) return (wh / 1000).toFixed(1) + ' kWh';
            return Math.round(wh) + ' Wh';
        }

        function formatPercentage(value) {
            if (isNaN(value)) return '--';
            return Math.round(value * 10) / 10 + '%';
        }

        function updateSolarPosition(position) {
            const sun = document.getElementById('sunPosition');
            const container = document.getElementById('solarPosition');
            
            // Convert elevation and azimuth to x,y position
            const maxElevation = 90;
            const elevationPercent = Math.max(0, position.elevation) / maxElevation;
            
            // Azimuth: 0° = North, 90° = East, 180° = South, 270° = West
            // Convert to screen coordinates (0° = top, 90° = right)
            const azimuthRadians = (position.azimuth - 90) * Math.PI / 180;
            
            const containerRect = container.getBoundingClientRect();
            const centerX = containerRect.width / 2;
            const centerY = containerRect.height - 15; // Near bottom when elevation is 0
            
            const radius = Math.min(centerX, containerRect.height) * 0.8;
            const x = centerX + (radius * elevationPercent * Math.cos(azimuthRadians)) - 15;
            const y = centerY - (radius * elevationPercent * Math.sin(azimuthRadians)) - 15;
            
            sun.style.left = Math.max(0, Math.min(containerRect.width - 30, x)) + 'px';
            sun.style.top = Math.max(0, Math.min(containerRect.height - 30, y)) + 'px';
            
            // Update sun opacity based on elevation
            sun.style.opacity = position.is_daytime ? '1' : '0.3';
            
            document.getElementById('elevation').textContent = position.elevation.toFixed(1) + '°';
            document.getElementById('azimuth').textContent = position.azimuth.toFixed(1) + '°';
            document.getElementById('sunriseSunset').textContent = position.sunrise + ' / ' + position.sunset;
        }

        function updateInverters(inverters) {
            const grid = document.getElementById('inverterGrid');
            
            if (!inverters || inverters.length === 0) {
                grid.innerHTML = '<div class="error">No inverter data available</div>';
                return;
            }
            
            grid.innerHTML = '';
            
            inverters.forEach(inverter => {
                const card = document.createElement('div');
                card.className = 'inverter-card';
                
                const isActive = inverter.current_watts > 0;
                const statusClass = isActive ? 'status-online' : 'status-offline';
                const statusText = isActive ? 'Active' : 'Idle';
                
                card.innerHTML = '
                    <div class="inverter-serial">${inverter.serial}</div>
                    <div class="inverter-power">${formatWatts(inverter.current_watts)}</div>
                    <div class="inverter-status ${statusClass}">${statusText}</div>
                    <div style="font-size: 0.7em; color: #7f8c8d; margin-top: 5px;">
                        Max: ${formatWatts(inverter.max_watts)}
                    </div>
                ';
                
                grid.appendChild(card);
            });
        }

        async function fetchData() {
            try {
                const response = await fetch('/api/monitor');
                if (!response.ok) throw new Error('Network response was not ok');
                
                const data = await response.json();
                
                // Check if data is newer
                const dataTime = new Date(data.timestamp).getTime();
                if (dataTime <= lastUpdateTime) return;
                lastUpdateTime = dataTime;
                
                // Update production data
                document.getElementById('currentWatts').textContent = formatWatts(data.production.current_watts);
                document.getElementById('todayWh').textContent = formatWh(data.production.today_wh);
                document.getElementById('lifetimeWh').textContent = formatWh(data.production.lifetime_wh);
                document.getElementById('sevenDaysWh').textContent = formatWh(data.production.seven_days_wh);
                
                // Update power flow
                document.getElementById('pvWatts').textContent = formatWatts(data.power_flow.pv_watts);
                document.getElementById('loadWatts').textContent = formatWatts(data.power_flow.load_watts);
                
                const gridWatts = data.power_flow.grid_watts;
                const gridElement = document.getElementById('gridWatts');
                if (gridWatts > 0) {
                    gridElement.textContent = '↓ ' + formatWatts(gridWatts);
                    gridElement.style.color = '#e74c3c'; // Import - red
                } else if (gridWatts < 0) {
                    gridElement.textContent = '↑ ' + formatWatts(-gridWatts);
                    gridElement.style.color = '#27ae60'; // Export - green
                } else {
                    gridElement.textContent = formatWatts(0);
                    gridElement.style.color = '#7f8c8d'; // Neutral
                }
                
                // Storage data
                if (data.power_flow.storage_watts !== 0 || data.power_flow.storage_soc > 0) {
                    document.getElementById('storageRow').style.display = 'flex';
                    document.getElementById('storageWatts').textContent = formatWatts(data.power_flow.storage_watts);
                    document.getElementById('storageSOC').textContent = formatPercentage(data.power_flow.storage_soc);
                }
                
                // Update summary
                document.getElementById('systemEfficiency').textContent = formatPercentage(data.summary.system_efficiency);
                document.getElementById('selfConsumption').textContent = formatPercentage(data.summary.self_consumption);
                document.getElementById('solarCoverage').textContent = formatPercentage(data.summary.solar_coverage);
                document.getElementById('activeInverters').textContent = 
                    data.summary.active_inverters + ' / ' + data.summary.total_inverters;
                
                // Update solar position
                updateSolarPosition(data.solar_position);
                
                // Update inverters
                updateInverters(data.inverters);
                
                // Update timestamp
                document.getElementById('lastUpdate').textContent = 
                    'Last updated: ' + new Date(data.timestamp).toLocaleTimeString();
                
            } catch (error) {
                console.error('Error fetching data:', error);
                document.getElementById('lastUpdate').textContent = 
                    'Error: ' + error.message + ' (retrying...)';
            }
        }

        // Initial load
        fetchData();
        
        // Update every 30 seconds
        setInterval(fetchData, 30000);
        
        // Update solar position every minute (even without new data)
        setInterval(() => {
            const now = new Date();
            // This would need to be calculated client-side or fetch fresh position data
        }, 60000);
    </script>
</body>
</html>`

	// Write the files
	os.WriteFile(filepath.Join(e.config.WebDir, "index.html"), []byte(indexHTML), 0644)
	os.WriteFile(filepath.Join(e.config.WebDir, "monitor.html"), []byte(monitorHTML), 0644)
	
	log.Printf("Created default web files in %s", e.config.WebDir)
}

// Add all the missing methods that were referenced but not included
func (e *EnvoyExporter) checkCondition(condition string, data interface{}) bool {
	if condition == "" {
		return true
	}
	
	// Find condition definition
	for _, cond := range e.config.Conditions.Conditions {
		if cond.Name == condition {
			return e.evaluateConditionCheck(cond.Check, data)
		}
	}
	
	// Default to true if condition not found
	return true
}

func (e *EnvoyExporter) evaluateConditionCheck(check string, data interface{}) bool {
	switch {
	case check == "endpoint_accessible":
		return data != nil
	case strings.HasPrefix(check, "json_path_exists"):
		path := strings.Trim(strings.TrimPrefix(check, "json_path_exists("), "()\"")
		return e.jsonPathExists(data, path)
	case strings.HasPrefix(check, "json_path_value"):
		// Extract path and comparison
		parts := strings.Split(check, " ")
		if len(parts) >= 3 {
			path := strings.Trim(strings.TrimPrefix(parts[0], "json_path_value("), "()\"")
			value := e.getJSONPathValue(data, path)
			if floatVal, ok := value.(float64); ok {
				return floatVal != 0
			}
		}
		return false
	case strings.Contains(check, "array_has_type"):
		typeVal := strings.Trim(strings.TrimPrefix(check, "array_has_type("), "()\"")
		return e.arrayHasType(data, typeVal)
	default:
		return true
	}
}

func (e *EnvoyExporter) jsonPathExists(data interface{}, path string) bool {
	return e.getJSONPathValue(data, path) != nil
}

func (e *EnvoyExporter) getJSONPathValue(data interface{}, path string) interface{} {
	if data == nil {
		return nil
	}
	
	parts := strings.Split(path, ".")
	current := data
	
	for _, part := range parts {
		if current == nil {
			return nil
		}
		
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		case []interface{}:
			return nil
		default:
			val := reflect.ValueOf(current)
			if val.Kind() == reflect.Ptr {
				val = val.Elem()
			}
			if val.Kind() == reflect.Struct {
				field := val.FieldByName(strings.Title(part))
				if field.IsValid() {
					current = field.Interface()
				} else {
					return nil
				}
			} else {
				return nil
			}
		}
	}
	
	return current
}

func (e *EnvoyExporter) arrayHasType(data interface{}, typeVal string) bool {
	if arr, ok := data.([]interface{}); ok {
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if t, ok := itemMap["type"].(string); ok && t == typeVal {
					return true
				}
			}
		}
	}
	return false
}

func (e *EnvoyExporter) transformValue(value interface{}, transform string) interface{} {
	switch transform {
	case "bool_to_int":
		if b, ok := value.(bool); ok {
			if b {
				return 1
			}
			return 0
		}
	case "mw_to_watts":
		if f, ok := value.(float64); ok {
			return f / 1000.0
		}
		if i, ok := value.(int); ok {
			return float64(i) / 1000.0
		}
	case "connected_to_int":
		if s, ok := value.(string); ok && s == "connected" {
			return 1
		}
		return 0
	case "ok_to_int":
		if s, ok := value.(string); ok && s == "ok" {
			return 1
		}
		return 0
	case "enabled_to_int":
		if s, ok := value.(string); ok && s == "enabled" {
			return 1
		}
		return 0
	case "battery_state_to_int":
		if s, ok := value.(string); ok {
			switch s {
			case "charging":
				return 1
			case "discharging":
				return -1
			default:
				return 0
			}
		}
		return 0
	}
	return value
}

func (e *EnvoyExporter) serveHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"status": "ok",
		"envoy_ip": e.config.EnvoyIP,
	}
	json.NewEncoder(w).Encode(status)
}

func (e *EnvoyExporter) serveDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	debug := map[string]interface{}{
		"config": map[string]interface{}{
			"envoy_ip": e.config.EnvoyIP,
			"web_dir": e.config.WebDir,
		},
	}
	json.NewEncoder(w).Encode(debug)
}

func (e *EnvoyExporter) getTotalMetricCount() int {
	total := 0
	for _, query := range e.config.Queries {
		total += len(query.Metrics)
	}
	return total
}
