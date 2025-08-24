package main

import (
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
//	"strconv"
	"strings"
	"sync"
	"time"
)

// Configuration structures
type Config struct {
	XMLName     xml.Name `xml:"envoy_config"`
	User        string   `xml:"user"`
	Password    string   `xml:"password"`
	EnvoySerial string   `xml:"envoy_serial"`
	EnvoyIP     string   `xml:"envoy_ip"`
	Port        string   `xml:"port"`
	Queries     []Query  `xml:"query"`
}

type Query struct {
	Name string `xml:"name,attr"`
	URL  string `xml:",chardata"`
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

// Envoy data structures
type ProductionData struct {
	WattHoursToday    int `json:"wattHoursToday"`
	WattHoursSevenDays int `json:"wattHoursSevenDays"`
	WattHoursLifetime int `json:"wattHoursLifetime"`
	WattsNow          int `json:"wattsNow"`
}

type Inverter struct {
	SerialNumber     string `json:"serialNumber"`
	LastReportDate   int64  `json:"lastReportDate"`
	DevType          int    `json:"devType"`
	LastReportWatts  int    `json:"lastReportWatts"`
	MaxReportWatts   int    `json:"maxReportWatts"`
}

type Meter struct {
	EID             int      `json:"eid"`
	State           string   `json:"state"`
	MeasurementType string   `json:"measurementType"`
	PhaseMode       string   `json:"phaseMode"`
	PhaseCount      int      `json:"phaseCount"`
	MeteringStatus  string   `json:"meteringStatus"`
	StatusFlags     []string `json:"statusFlags"`
}

type LiveDataStatus struct {
	Connection struct {
		MQTTState string `json:"mqtt_state"`
		ProvState string `json:"prov_state"`
		AuthState string `json:"auth_state"`
	} `json:"connection"`
	Meters struct {
		LastUpdate int64 `json:"last_update"`
		PV         struct {
			AggPMW   int `json:"agg_p_mw"`
			AggSMVA  int `json:"agg_s_mva"`
		} `json:"pv"`
		Grid struct {
			AggPMW  int `json:"agg_p_mw"`
			AggSMVA int `json:"agg_s_mva"`
		} `json:"grid"`
		Load struct {
			AggPMW  int `json:"agg_p_mw"`
			AggSMVA int `json:"agg_s_mva"`
		} `json:"load"`
	} `json:"meters"`
}

type EnvoyExporter struct {
	config       Config
	token        string
	tokenExpires int64
	tokenMutex   sync.RWMutex
	httpClient   *http.Client
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

	// Create HTTP client with insecure TLS (for self-signed certs)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	exporter := &EnvoyExporter{
		config:     config,
		httpClient: client,
	}

	// Get initial token
	err = exporter.refreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial token: %w", err)
	}

	// Start token refresh goroutine
	go exporter.tokenRefreshLoop()

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
		e.token = string(tokenBody)
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

func (e *EnvoyExporter) getToken() string {
	e.tokenMutex.RLock()
	defer e.tokenMutex.RUnlock()
	return e.token
}

func (e *EnvoyExporter) makeEnvoyRequest(endpoint string) ([]byte, error) {
	url := strings.ReplaceAll(endpoint, "{envoy_ip}", e.config.EnvoyIP)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.getToken())

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (e *EnvoyExporter) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var metrics strings.Builder

	for _, query := range e.config.Queries {
		data, err := e.makeEnvoyRequest(query.URL)
		if err != nil {
			log.Printf("Failed to query %s: %v", query.Name, err)
			continue
		}

		switch query.Name {
		case "production_meter":
			var prod ProductionData
			if err := json.Unmarshal(data, &prod); err == nil {
				metrics.WriteString(fmt.Sprintf("# HELP envoy_production_watts_now Current production in watts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_production_watts_now gauge\n"))
				metrics.WriteString(fmt.Sprintf("envoy_production_watts_now %d\n", prod.WattsNow))
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_production_wh_today Production today in watt-hours\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_production_wh_today counter\n"))
				metrics.WriteString(fmt.Sprintf("envoy_production_wh_today %d\n", prod.WattHoursToday))
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_production_wh_lifetime Lifetime production in watt-hours\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_production_wh_lifetime counter\n"))
				metrics.WriteString(fmt.Sprintf("envoy_production_wh_lifetime %d\n", prod.WattHoursLifetime))
			}

		case "inverters":
			var inverters []Inverter
			if err := json.Unmarshal(data, &inverters); err == nil {
				metrics.WriteString(fmt.Sprintf("# HELP envoy_inverter_watts Current inverter output in watts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_inverter_watts gauge\n"))
				for _, inv := range inverters {
					metrics.WriteString(fmt.Sprintf("envoy_inverter_watts{serial=\"%s\"} %d\n", inv.SerialNumber, inv.LastReportWatts))
				}
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_inverter_max_watts Maximum inverter output in watts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_inverter_max_watts gauge\n"))
				for _, inv := range inverters {
					metrics.WriteString(fmt.Sprintf("envoy_inverter_max_watts{serial=\"%s\"} %d\n", inv.SerialNumber, inv.MaxReportWatts))
				}
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_inverter_last_report_timestamp Last report timestamp\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_inverter_last_report_timestamp gauge\n"))
				for _, inv := range inverters {
					metrics.WriteString(fmt.Sprintf("envoy_inverter_last_report_timestamp{serial=\"%s\"} %d\n", inv.SerialNumber, inv.LastReportDate))
				}
			}

		case "meters":
			var meters []Meter
			if err := json.Unmarshal(data, &meters); err == nil {
				metrics.WriteString(fmt.Sprintf("# HELP envoy_meter_enabled Meter enabled status\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_meter_enabled gauge\n"))
				for _, meter := range meters {
					enabled := 0
					if meter.State == "enabled" {
						enabled = 1
					}
					metrics.WriteString(fmt.Sprintf("envoy_meter_enabled{eid=\"%d\",type=\"%s\",phase_mode=\"%s\"} %d\n", 
						meter.EID, meter.MeasurementType, meter.PhaseMode, enabled))
				}
			}

		case "livedata":
			var liveData LiveDataStatus
			if err := json.Unmarshal(data, &liveData); err == nil {
				metrics.WriteString(fmt.Sprintf("# HELP envoy_pv_power_mw PV power in milliwatts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_pv_power_mw gauge\n"))
				metrics.WriteString(fmt.Sprintf("envoy_pv_power_mw %d\n", liveData.Meters.PV.AggPMW))
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_grid_power_mw Grid power in milliwatts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_grid_power_mw gauge\n"))
				metrics.WriteString(fmt.Sprintf("envoy_grid_power_mw %d\n", liveData.Meters.Grid.AggPMW))
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_load_power_mw Load power in milliwatts\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_load_power_mw gauge\n"))
				metrics.WriteString(fmt.Sprintf("envoy_load_power_mw %d\n", liveData.Meters.Load.AggPMW))
				
				metrics.WriteString(fmt.Sprintf("# HELP envoy_connection_status Connection status\n"))
				metrics.WriteString(fmt.Sprintf("# TYPE envoy_connection_status gauge\n"))
				mqttConnected := 0
				if liveData.Connection.MQTTState == "connected" {
					mqttConnected = 1
				}
				authOK := 0
				if liveData.Connection.AuthState == "ok" {
					authOK = 1
				}
				metrics.WriteString(fmt.Sprintf("envoy_connection_status{type=\"mqtt\"} %d\n", mqttConnected))
				metrics.WriteString(fmt.Sprintf("envoy_connection_status{type=\"auth\"} %d\n", authOK))
			}
		}
	}

	// Add exporter info
	metrics.WriteString(fmt.Sprintf("# HELP envoy_exporter_up Exporter up status\n"))
	metrics.WriteString(fmt.Sprintf("# TYPE envoy_exporter_up gauge\n"))
	metrics.WriteString(fmt.Sprintf("envoy_exporter_up 1\n"))
	
	metrics.WriteString(fmt.Sprintf("# HELP envoy_token_expires_timestamp Token expiry timestamp\n"))
	metrics.WriteString(fmt.Sprintf("# TYPE envoy_token_expires_timestamp gauge\n"))
	metrics.WriteString(fmt.Sprintf("envoy_token_expires_timestamp %d\n", e.tokenExpires))

	w.Write([]byte(metrics.String()))
}

func (e *EnvoyExporter) serveHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	e.tokenMutex.RLock()
	tokenValid := time.Now().Unix() < e.tokenExpires
	e.tokenMutex.RUnlock()
	
	status := map[string]interface{}{
		"status":      "ok",
		"token_valid": tokenValid,
		"token_expires": time.Unix(e.tokenExpires, 0).Format(time.RFC3339),
	}
	
	json.NewEncoder(w).Encode(status)
}

func main() {
	configFile := "envoy_config.xml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	exporter, err := NewEnvoyExporter(configFile)
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	http.HandleFunc("/metrics", exporter.serveMetrics)
	http.HandleFunc("/health", exporter.serveHealth)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html>
<head><title>Envoy Prometheus Exporter</title></head>
<body>
<h1>Envoy Prometheus Exporter</h1>
<p><a href="/metrics">Metrics</a></p>
<p><a href="/health">Health</a></p>
</body>
</html>`)
	})

	listenAddr := ":" + exporter.config.Port
	log.Printf("Starting server on %s", listenAddr)
	log.Printf("Envoy IP: %s", exporter.config.EnvoyIP)
	log.Printf("Configured queries: %d", len(exporter.config.Queries))
	
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
