package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

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
	
	// Basic system health
	status := map[string]interface{}{
		"status":    "ok",
		"envoy_ip":  e.config.EnvoyIP,
		"timestamp": time.Now().Unix(),
	}

	// Add production tracker status
	if e.productionTracker != nil {
		status["production_tracking"] = map[string]interface{}{
			"enabled": true,
			"days_stored": len(e.productionTracker.history.Days),
			"last_cleanup": e.productionTracker.history.LastCleanup,
		}
	} else {
		status["production_tracking"] = map[string]interface{}{
			"enabled": false,
		}
	}

	// Add MQTT status
	mqttStatus := map[string]interface{}{
		"enabled": e.config.MQTT.Enabled,
	}
	
	if e.config.MQTT.Enabled {
		mqttStatus["broker"] = fmt.Sprintf("%s:%d", e.config.MQTT.Broker, e.config.MQTT.Port)
		mqttStatus["topic_prefix"] = e.config.MQTT.TopicPrefix
		mqttStatus["publish_interval"] = e.config.MQTT.PublishInterval
		
		if e.mqttPublisher != nil {
			mqttStatus["connected"] = e.mqttPublisher.IsConnected()
			mqttStatus["last_publish"] = e.mqttPublisher.lastPublish
			
			// Add time since last publish
			if e.mqttPublisher.lastPublish > 0 {
				mqttStatus["seconds_since_publish"] = time.Now().Unix() - e.mqttPublisher.lastPublish
			}
		} else {
			mqttStatus["connected"] = false
			mqttStatus["last_publish"] = 0
		}
	}
	
	status["mqtt"] = mqttStatus

	// Add monitor data freshness
	e.monitorMutex.RLock()
	lastMonitorUpdate := e.lastMonitorData.Timestamp
	e.monitorMutex.RUnlock()
	
	if !lastMonitorUpdate.IsZero() {
		status["monitor_data"] = map[string]interface{}{
			"last_update": lastMonitorUpdate.Unix(),
			"seconds_ago": int(time.Since(lastMonitorUpdate).Seconds()),
			"fresh": time.Since(lastMonitorUpdate) < 60*time.Second,
		}
	} else {
		status["monitor_data"] = map[string]interface{}{
			"last_update": 0,
			"fresh": false,
		}
	}

	// Token status
	e.tokenMutex.RLock()
	tokenExpires := e.tokenExpires
	hasToken := e.token != ""
	e.tokenMutex.RUnlock()
	
	status["authentication"] = map[string]interface{}{
		"has_token": hasToken,
		"token_expires": tokenExpires,
		"token_valid": hasToken && tokenExpires > time.Now().Unix(),
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
