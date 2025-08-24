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
	"reflect"
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

type EnvoyExporter struct {
	config       Config
	token        string
	tokenExpires int64
	tokenMutex   sync.RWMutex
	httpClient   *http.Client
	metricCache  map[string]float64
	cacheMutex   sync.RWMutex
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
		config:      config,
		httpClient:  client,
		metricCache: make(map[string]float64),
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
			// Handle array access if needed
			return nil
		default:
			// Use reflection for structs
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

func (e *EnvoyExporter) processMetric(metric Metric, data interface{}, metrics *strings.Builder) {
	// Check condition
	if !e.checkCondition(metric.Condition, data) {
		return
	}
	
	// Write help and type
	metrics.WriteString(fmt.Sprintf("# HELP %s %s\n", metric.Name, metric.Help))
	metrics.WriteString(fmt.Sprintf("# TYPE %s %s\n", metric.Name, metric.Type))
	
	// Handle different field configurations
	if len(metric.Fields) == 0 {
		// Static value metric
		value := "1"
		if metric.Value != "" {
			value = metric.Value
		}
		metrics.WriteString(fmt.Sprintf("%s %s\n", metric.Name, value))
		return
	}
	
	// Process fields
	labels := make(map[string]string)
	var metricValue interface{}
	
	for _, field := range metric.Fields {
		if field.Label != "" {
			// This field is a label
			labelValue := e.getJSONPathValue(data, field.JSONPath)
			if field.LabelValue != "" {
				labels[field.Label] = field.LabelValue
			} else if labelValue != nil {
				labels[field.Label] = fmt.Sprintf("%v", labelValue)
			}
		} else {
			// This field is the metric value
			metricValue = e.getJSONPathValue(data, field.JSONPath)
			if field.Transform != "" {
				metricValue = e.transformValue(metricValue, field.Transform)
			}
		}
	}
	
	// Handle special transforms that need multiple values
	for _, field := range metric.Fields {
		if field.Transform == "signal_strength_percentage" && strings.Contains(field.JSONPath, ",") {
			paths := strings.Split(field.JSONPath, ",")
			if len(paths) == 2 {
				strength := e.getJSONPathValue(data, paths[0])
				maxStrength := e.getJSONPathValue(data, paths[1])
				if s, ok := strength.(float64); ok {
					if m, ok := maxStrength.(float64); ok && m > 0 {
						metricValue = (s / m) * 100
					}
				}
			}
		}
	}
	
	// Use static value if no fields provided a value
	if metricValue == nil && metric.Value != "" {
		metricValue = metric.Value
	}
	
	// Format labels
	labelStr := ""
	if len(labels) > 0 {
		labelParts := make([]string, 0, len(labels))
		for key, value := range labels {
			labelParts = append(labelParts, fmt.Sprintf("%s=\"%s\"", key, value))
		}
		labelStr = "{" + strings.Join(labelParts, ",") + "}"
	}
	
	// Output metric
	if metricValue != nil {
		metrics.WriteString(fmt.Sprintf("%s%s %v\n", metric.Name, labelStr, metricValue))
		
		// Cache metric for calculated metrics
		e.cacheMutex.Lock()
		if f, ok := metricValue.(float64); ok {
			e.metricCache[metric.Name] = f
		} else if i, ok := metricValue.(int); ok {
			e.metricCache[metric.Name] = float64(i)
		} else if s, ok := metricValue.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				e.metricCache[metric.Name] = f
			}
		}
		e.cacheMutex.Unlock()
	}
}

func (e *EnvoyExporter) processArrayMetrics(metric Metric, dataArray []interface{}, metrics *strings.Builder) {
	for _, item := range dataArray {
		e.processMetric(metric, item, metrics)
	}
}

func (e *EnvoyExporter) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var metrics strings.Builder
	
	// Clear metric cache
	e.cacheMutex.Lock()
	e.metricCache = make(map[string]float64)
	e.cacheMutex.Unlock()

	// Process all configured queries
	for _, query := range e.config.Queries {
		data, err := e.makeEnvoyRequest(query.URL)
		if err != nil {
			log.Printf("Failed to query %s: %v", query.Name, err)
			continue
		}

		// Parse JSON data
		var jsonData interface{}
		if err := json.Unmarshal(data, &jsonData); err != nil {
			log.Printf("Failed to parse JSON for %s: %v", query.Name, err)
			continue
		}
		
		// Check if endpoint is accessible (for condition evaluation)
		if !e.checkCondition(query.Condition, jsonData) {
			log.Printf("Condition not met for query %s, skipping", query.Name)
			continue
		}

		// Process metrics for this query
		for _, metric := range query.Metrics {
			if query.Array {
				if arr, ok := jsonData.([]interface{}); ok {
					e.processArrayMetrics(metric, arr, &metrics)
				}
			} else {
				e.processMetric(metric, jsonData, &metrics)
			}
		}
	}
	
	// Process calculated metrics
	e.processCalculatedMetrics(&metrics)

	// Add exporter info
	metrics.WriteString("# HELP envoy_exporter_up Exporter up status\n")
	metrics.WriteString("# TYPE envoy_exporter_up gauge\n")
	metrics.WriteString("envoy_exporter_up 1\n")
	
	metrics.WriteString("# HELP envoy_token_expires_timestamp Token expiry timestamp\n")
	metrics.WriteString("# TYPE envoy_token_expires_timestamp gauge\n")
	metrics.WriteString(fmt.Sprintf("envoy_token_expires_timestamp %d\n", e.tokenExpires))
	
	metrics.WriteString("# HELP envoy_scrape_timestamp Timestamp of this scrape\n")
	metrics.WriteString("# TYPE envoy_scrape_timestamp gauge\n")
	metrics.WriteString(fmt.Sprintf("envoy_scrape_timestamp %d\n", time.Now().Unix()))

	w.Write([]byte(metrics.String()))
}

func (e *EnvoyExporter) processCalculatedMetrics(metrics *strings.Builder) {
	e.cacheMutex.RLock()
	defer e.cacheMutex.RUnlock()
	
	for _, calc := range e.config.CalculatedMetrics.Metrics {
		// Check condition
		if !e.checkCalculatedCondition(calc.Condition) {
			continue
		}
		
		value := e.evaluateCalculation(calc.Calculation)
		if !math.IsNaN(value) {
			metrics.WriteString(fmt.Sprintf("# HELP %s %s\n", calc.Name, calc.Help))
			metrics.WriteString(fmt.Sprintf("# TYPE %s %s\n", calc.Name, calc.Type))
			metrics.WriteString(fmt.Sprintf("%s %.2f\n", calc.Name, value))
		}
	}
}

func (e *EnvoyExporter) checkCalculatedCondition(condition string) bool {
	if condition == "" {
		return true
	}
	
	switch condition {
	case "pv_producing":
		return e.metricCache["envoy_pv_power_watts"] > 0
	case "load_present":
		return e.metricCache["envoy_load_power_watts"] > 0
	case "storage_present":
		_, exists := e.metricCache["envoy_storage_power_watts"]
		return exists
	default:
		return true
	}
}

func (e *EnvoyExporter) evaluateCalculation(calc string) float64 {
	// Simple expression evaluator for basic calculations
	// Replace metric names with values
	expression := calc
	for metricName, value := range e.metricCache {
		expression = strings.ReplaceAll(expression, metricName, fmt.Sprintf("%.2f", value))
	}
	
	// Handle functions
	expression = e.replaceFunctions(expression)
	
	// Basic expression evaluation (simplified)
	return e.evaluateExpression(expression)
}

func (e *EnvoyExporter) replaceFunctions(expr string) string {
	// Handle max(0, value)
	for strings.Contains(expr, "max(0,") {
		start := strings.Index(expr, "max(0,")
		if start == -1 {
			break
		}
		
		// Find matching closing parenthesis
		depth := 0
		end := start + 6
		for i := start + 6; i < len(expr); i++ {
			if expr[i] == '(' {
				depth++
			} else if expr[i] == ')' {
				if depth == 0 {
					end = i
					break
				}
				depth--
			}
		}
		
		// Extract the value part
		valuePart := strings.TrimSpace(expr[start+6 : end])
		value := e.evaluateExpression(valuePart)
		result := math.Max(0, value)
		
		expr = expr[:start] + fmt.Sprintf("%.2f", result) + expr[end+1:]
	}
	
	// Handle clamp(min, max, value)
	for strings.Contains(expr, "clamp(") {
		start := strings.Index(expr, "clamp(")
		if start == -1 {
			break
		}
		
		depth := 0
		end := start + 6
		for i := start + 6; i < len(expr); i++ {
			if expr[i] == '(' {
				depth++
			} else if expr[i] == ')' {
				if depth == 0 {
					end = i
					break
				}
				depth--
			}
		}
		
		// Parse clamp arguments
		args := strings.Split(expr[start+6:end], ",")
		if len(args) == 3 {
			min := e.evaluateExpression(strings.TrimSpace(args[0]))
			max := e.evaluateExpression(strings.TrimSpace(args[1]))
			value := e.evaluateExpression(strings.TrimSpace(args[2]))
			result := math.Max(min, math.Min(max, value))
			expr = expr[:start] + fmt.Sprintf("%.2f", result) + expr[end+1:]
		}
	}
	
	// Handle coalesce(value, default)
	for strings.Contains(expr, "coalesce(") {
		start := strings.Index(expr, "coalesce(")
		if start == -1 {
			break
		}
		
		depth := 0
		end := start + 9
		for i := start + 9; i < len(expr); i++ {
			if expr[i] == '(' {
				depth++
			} else if expr[i] == ')' {
				if depth == 0 {
					end = i
					break
				}
				depth--
			}
		}
		
		args := strings.Split(expr[start+9:end], ",")
		if len(args) == 2 {
			value := e.evaluateExpression(strings.TrimSpace(args[0]))
			defaultVal := e.evaluateExpression(strings.TrimSpace(args[1]))
			if math.IsNaN(value) || value == 0 {
				value = defaultVal
			}
			expr = expr[:start] + fmt.Sprintf("%.2f", value) + expr[end+1:]
		}
	}
	
	return expr
}

func (e *EnvoyExporter) evaluateExpression(expr string) float64 {
	expr = strings.TrimSpace(expr)
	
	// Handle simple arithmetic operations
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) == 2 {
			left := e.evaluateExpression(strings.TrimSpace(parts[0]))
			right := e.evaluateExpression(strings.TrimSpace(parts[1]))
			return left + right
		}
	}
	
	if strings.Contains(expr, "-") && !strings.HasPrefix(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) == 2 {
			left := e.evaluateExpression(strings.TrimSpace(parts[0]))
			right := e.evaluateExpression(strings.TrimSpace(parts[1]))
			return left - right
		}
	}
	
	if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) == 2 {
			left := e.evaluateExpression(strings.TrimSpace(parts[0]))
			right := e.evaluateExpression(strings.TrimSpace(parts[1]))
			return left * right
		}
	}
	
	if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) == 2 {
			left := e.evaluateExpression(strings.TrimSpace(parts[0]))
			right := e.evaluateExpression(strings.TrimSpace(parts[1]))
			if right != 0 {
				return left / right
			}
		}
	}
	
	// Try to parse as number
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f
	}
	
	return math.NaN()
}

func (e *EnvoyExporter) serveHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	e.tokenMutex.RLock()
	tokenValid := time.Now().Unix() < e.tokenExpires
	e.tokenMutex.RUnlock()
	
	// Test connectivity to Envoy
	envoyReachable := true
	testURL := strings.ReplaceAll("https://{envoy_ip}/info", "{envoy_ip}", e.config.EnvoyIP)
	_, err := e.makeEnvoyRequest(testURL)
	if err != nil {
		envoyReachable = false
	}
	
	status := map[string]interface{}{
		"status":             "ok",
		"token_valid":        tokenValid,
		"token_expires":      time.Unix(e.tokenExpires, 0).Format(time.RFC3339),
		"envoy_reachable":    envoyReachable,
		"envoy_ip":           e.config.EnvoyIP,
		"configured_queries": len(e.config.Queries),
	}
	
	// If token is expired or Envoy is unreachable, mark as degraded
	if !tokenValid || !envoyReachable {
		status["status"] = "degraded"
	}
	
	json.NewEncoder(w).Encode(status)
}

func (e *EnvoyExporter) serveDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	debug := map[string]interface{}{
		"config": map[string]interface{}{
			"envoy_ip":      e.config.EnvoyIP,
			"envoy_serial":  e.config.EnvoySerial,
			"port":          e.config.Port,
			"query_count":   len(e.config.Queries),
		},
		"token_info": map[string]interface{}{
			"has_token":     e.getToken() != "",
			"token_expires": time.Unix(e.tokenExpires, 0).Format(time.RFC3339),
			"token_valid":   time.Now().Unix() < e.tokenExpires,
		},
		"queries": make([]map[string]interface{}, 0),
	}
	
	// Test each query endpoint
	for _, query := range e.config.Queries {
		queryInfo := map[string]interface{}{
			"name":        query.Name,
			"url":         strings.ReplaceAll(query.URL, "{envoy_ip}", e.config.EnvoyIP),
			"metric_count": len(query.Metrics),
			"array":       query.Array,
			"condition":   query.Condition,
		}
		
		data, err := e.makeEnvoyRequest(query.URL)
		if err != nil {
			queryInfo["status"] = "error"
			queryInfo["error"] = err.Error()
		} else {
			queryInfo["status"] = "ok"
			queryInfo["response_size"] = len(data)
			
			// Try to parse as JSON to validate structure
			var jsonData interface{}
			if json.Unmarshal(data, &jsonData) == nil {
				queryInfo["valid_json"] = true
				queryInfo["condition_met"] = e.checkCondition(query.Condition, jsonData)
			} else {
				queryInfo["valid_json"] = false
			}
		}
		
		debug["queries"] = append(debug["queries"].([]map[string]interface{}), queryInfo)
	}
	
	// Add metric cache info
	e.cacheMutex.RLock()
	debug["metric_cache"] = e.metricCache
	e.cacheMutex.RUnlock()
	
	json.NewEncoder(w).Encode(debug)
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

	// Set up HTTP routes
	http.HandleFunc("/metrics", exporter.serveMetrics)
	http.HandleFunc("/health", exporter.serveHealth)
	http.HandleFunc("/debug", exporter.serveDebug)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html>
<head><title>Configuration-Driven Envoy Prometheus Exporter</title></head>
<body>
<h1>Configuration-Driven Envoy Prometheus Exporter</h1>
<h2>Endpoints</h2>
<ul>
<li><a href="/metrics">Metrics</a> - Prometheus metrics endpoint</li>
<li><a href="/health">Health</a> - Health check endpoint</li>
<li><a href="/debug">Debug</a> - Debug information and query status</li>
</ul>
<h2>System Information</h2>
<p><strong>Envoy IP:</strong> %s</p>
<p><strong>Port:</strong> %s</p>
<p><strong>Configured Queries:</strong> %d</p>
<p><strong>Total Metrics:</strong> %d</p>
<p><strong>Calculated Metrics:</strong> %d</p>
<h2>Features</h2>
<ul>
<li>✅ Configuration-driven metrics (no code changes needed)</li>
<li>✅ JSON path-based field extraction</li>
<li>✅ Conditional metric generation</li>
<li>✅ Built-in data transformations</li>
<li>✅ Calculated/derived metrics</li>
<li>✅ Array and object data handling</li>
<li>✅ Comprehensive error handling</li>
<li>✅ Debug endpoint for troubleshooting</li>
</ul>
<h2>Configuration</h2>
<p>All metrics are defined in the XML configuration file. To add new metrics:</p>
<ol>
<li>Add the endpoint URL as a new query</li>
<li>Define metrics with JSON paths to extract data</li>
<li>Use transforms to convert data types</li>
<li>Add conditions for optional features</li>
<li>Restart the exporter</li>
</ol>
</body>
</html>`, exporter.config.EnvoyIP, exporter.config.Port, len(exporter.config.Queries), exporter.getTotalMetricCount(), len(exporter.config.CalculatedMetrics.Metrics))
	})

	listenAddr := ":" + exporter.config.Port
	log.Printf("Starting Configuration-Driven Envoy Prometheus Exporter on %s", listenAddr)
	log.Printf("Envoy IP: %s", exporter.config.EnvoyIP)
	log.Printf("Envoy Serial: %s", exporter.config.EnvoySerial)
	log.Printf("Configured queries: %d", len(exporter.config.Queries))
	log.Printf("Total metrics: %d", exporter.getTotalMetricCount())
	log.Printf("Calculated metrics: %d", len(exporter.config.CalculatedMetrics.Metrics))
	
	// Log configured features
	features := []string{}
	for _, query := range exporter.config.Queries {
		switch query.Name {
		case "batteries":
			features = append(features, "Battery Storage")
		case "tariff":
			features = append(features, "Tariff Management")
		case "device_status":
			features = append(features, "Device Health")
		case "wireless_connection":
			features = append(features, "Network Status")
		case "consumption_totals":
			features = append(features, "Enhanced Consumption")
		}
	}
	
	if len(features) > 0 {
		log.Printf("Enhanced features configured: %s", strings.Join(features, ", "))
	}
	
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func (e *EnvoyExporter) getTotalMetricCount() int {
	total := 0
	for _, query := range e.config.Queries {
		total += len(query.Metrics)
	}
	return total
}
