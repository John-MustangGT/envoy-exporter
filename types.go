package main

import (
	"encoding/xml"
	"net/http"
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
	MQTT               MQTTConfig          `xml:"mqtt"`                // ADD THIS LINE
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

// MQTT configuration structure
type MQTTConfig struct {
	Enabled         bool   `xml:"enabled,attr"`
	Broker          string `xml:"broker"`
	Port            int    `xml:"port"`
	Username        string `xml:"username"`
	Password        string `xml:"password"`
	ClientID        string `xml:"client_id"`
	TopicPrefix     string `xml:"topic_prefix"`
	QoS             byte   `xml:"qos"`
	Retain          bool   `xml:"retain"`
	TLS             bool   `xml:"tls"`
	InsecureTLS     bool   `xml:"insecure_tls"`
	PublishInterval int    `xml:"publish_interval"` // seconds, default 60
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
	config            Config
	token             string
	tokenExpires      int64
	tokenMutex        sync.RWMutex
	httpClient        *http.Client
	metricCache       map[string]float64
	cacheMutex        sync.RWMutex
	queryResults      map[string]QueryResult
	resultsMutex      sync.RWMutex
	lastMonitorData   MonitorData
	monitorMutex      sync.RWMutex
	productionTracker *ProductionTracker
	mqttPublisher     *MQTTPublisher    // ADD THIS LINE
}
