package main

import (
	"net/http"
	"strings"
	"strconv"
	"log"
	"fmt"
	"encoding/json"
	"math"
	"time"
)

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

