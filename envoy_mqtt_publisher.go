// mqtt_publisher.go - MQTT publishing functionality
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTT Publisher
type MQTTPublisher struct {
	config       MQTTConfig
	client       mqtt.Client
	connected    bool
	mutex        sync.RWMutex
	lastPublish  int64
	shutdown     chan struct{}
}

// Default MQTT metrics to publish
type MQTTMetrics struct {
	Timestamp        int64   `json:"timestamp"`
	CurrentWatts     float64 `json:"current_watts"`
	TodayWh          float64 `json:"today_wh"`
	LifetimeWh       float64 `json:"lifetime_wh"`
	InvertersOnline  int     `json:"inverters_online"`
	InvertersTotal   int     `json:"inverters_total"`
	GridWatts        float64 `json:"grid_watts"`
	LoadWatts        float64 `json:"load_watts"`
	SystemEfficiency float64 `json:"system_efficiency"`
	SelfConsumption  float64 `json:"self_consumption"`
	SolarCoverage    float64 `json:"solar_coverage"`
}

// Initialize MQTT publisher
func (e *EnvoyExporter) initMQTTPublisher() {
	if !e.config.MQTT.Enabled {
		LogInfo("MQTT publishing disabled")
		return
	}

	// Set default values
	if e.config.MQTT.ClientID == "" {
		e.config.MQTT.ClientID = fmt.Sprintf("envoy-exporter-%d", time.Now().Unix())
	}
	if e.config.MQTT.TopicPrefix == "" {
		e.config.MQTT.TopicPrefix = "solar/envoy"
	}
	if e.config.MQTT.QoS == 0 && e.config.MQTT.QoS > 2 {
		e.config.MQTT.QoS = 1 // Default to QoS 1
	}
	if e.config.MQTT.PublishInterval == 0 {
		e.config.MQTT.PublishInterval = 60 // Default to 60 seconds
	}

	publisher := &MQTTPublisher{
		config:   e.config.MQTT,
		shutdown: make(chan struct{}),
	}

	// Create MQTT client options
	opts := mqtt.NewClientOptions()
	brokerURL := fmt.Sprintf("tcp://%s:%d", e.config.MQTT.Broker, e.config.MQTT.Port)
	if e.config.MQTT.TLS {
		brokerURL = fmt.Sprintf("ssl://%s:%d", e.config.MQTT.Broker, e.config.MQTT.Port)
		tlsConfig := &tls.Config{
			InsecureSkipVerify: e.config.MQTT.InsecureTLS,
		}
		opts.SetTLSConfig(tlsConfig)
	}

	opts.AddBroker(brokerURL)
	opts.SetClientID(e.config.MQTT.ClientID)
	opts.SetUsername(e.config.MQTT.Username)
	opts.SetPassword(e.config.MQTT.Password)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetryInterval(10 * time.Second)
	opts.SetMaxReconnectInterval(60 * time.Second)
	opts.SetCleanSession(true)
	opts.SetKeepAlive(60 * time.Second)

	// Connection handlers
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		publisher.mutex.Lock()
		publisher.connected = true
		publisher.mutex.Unlock()
		LogInfo("MQTT: Connected to broker %s", brokerURL)
		
		// Publish online status
		publisher.publishStatus("online")
	})

	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		publisher.mutex.Lock()
		publisher.connected = false
		publisher.mutex.Unlock()
		LogInfo("MQTT: Connection lost: %v", err)
	})

	// Will message for clean disconnection detection
	willTopic := publisher.config.TopicPrefix + "/status"
	opts.SetWill(willTopic, "offline", publisher.config.QoS, publisher.config.Retain)

	publisher.client = mqtt.NewClient(opts)

	// Connect to broker
	if token := publisher.client.Connect(); token.Wait() && token.Error() != nil {
		LogInfo("MQTT: Failed to connect to broker: %v", token.Error())
		return
	}

	// Start publishing goroutine
	go publisher.publishLoop(e)

	e.mqttPublisher = publisher
	LogInfo("MQTT publisher initialized - broker: %s, topic prefix: %s, interval: %ds", 
		brokerURL, e.config.MQTT.TopicPrefix, e.config.MQTT.PublishInterval)
}

// Publishing loop
func (mp *MQTTPublisher) publishLoop(exporter *EnvoyExporter) {
	LogInfo("MQTT: Starting publish loop with %d second interval", mp.config.PublishInterval)
	
	ticker := time.NewTicker(time.Duration(mp.config.PublishInterval) * time.Second)
	defer ticker.Stop()

	// Publish immediately on startup
	mp.publishMetrics(exporter)

	for {
		select {
		case <-ticker.C:
			mp.publishMetrics(exporter)

		case <-mp.shutdown:
			LogInfo("MQTT: Publish loop shutdown requested")
			mp.publishStatus("offline")
			mp.client.Disconnect(1000) // Wait up to 1 second for clean disconnect
			return
		}
	}
}

// Publish current metrics
func (mp *MQTTPublisher) publishMetrics(exporter *EnvoyExporter) {
	mp.mutex.RLock()
	connected := mp.connected
	mp.mutex.RUnlock()

	if !connected {
		LogInfo("MQTT: Not connected, skipping publish")
		return
	}

	// Get current monitor data
	exporter.monitorMutex.RLock()
	monitorData := exporter.lastMonitorData
	exporter.monitorMutex.RUnlock()

	// Create metrics payload
	metrics := MQTTMetrics{
		Timestamp:        time.Now().Unix(),
		CurrentWatts:     monitorData.Production.CurrentWatts,
		TodayWh:          monitorData.Production.TodayWh,
		LifetimeWh:       monitorData.Production.LifetimeWh,
		InvertersOnline:  monitorData.Summary.ActiveInverters,
		InvertersTotal:   monitorData.Summary.TotalInverters,
		GridWatts:        monitorData.PowerFlow.GridWatts,
		LoadWatts:        monitorData.PowerFlow.LoadWatts,
		SystemEfficiency: monitorData.Summary.SystemEfficiency,
		SelfConsumption:  monitorData.Summary.SelfConsumption,
		SolarCoverage:    monitorData.Summary.SolarCoverage,
	}

	// Publish as JSON payload to main topic
	mp.publishJSON("metrics", metrics)

	// Publish individual metrics for easier consumption
	mp.publishFloat("current_watts", metrics.CurrentWatts)
	mp.publishFloat("today_wh", metrics.TodayWh)
	mp.publishFloat("lifetime_wh", metrics.LifetimeWh)
	mp.publishInt("inverters_online", metrics.InvertersOnline)
	mp.publishInt("inverters_total", metrics.InvertersTotal)
	mp.publishFloat("grid_watts", metrics.GridWatts)
	mp.publishFloat("load_watts", metrics.LoadWatts)
	mp.publishFloat("system_efficiency", metrics.SystemEfficiency)
	mp.publishFloat("self_consumption", metrics.SelfConsumption)
	mp.publishFloat("solar_coverage", metrics.SolarCoverage)

	// Publish power flow direction
	powerFlow := "idle"
	if metrics.GridWatts > 10 {
		powerFlow = "importing"
	} else if metrics.GridWatts < -10 {
		powerFlow = "exporting"
	}
	mp.publishString("power_flow", powerFlow)

	// Publish system status
	systemStatus := "offline"
	if metrics.InvertersOnline > 0 {
		systemStatus = "producing"
	} else if monitorData.SolarPosition.IsDaytime {
		systemStatus = "daylight"
	} else {
		systemStatus = "night"
	}
	mp.publishString("system_status", systemStatus)

	mp.lastPublish = time.Now().Unix()
	LogInfo("MQTT: Published metrics - Power: %.1fW, Inverters: %d/%d, Grid: %.1fW", 
		metrics.CurrentWatts, metrics.InvertersOnline, metrics.InvertersTotal, metrics.GridWatts)
}

// Helper functions for publishing different data types
func (mp *MQTTPublisher) publishJSON(subtopic string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		LogInfo("MQTT: Error marshaling JSON for %s: %v", subtopic, err)
		return
	}
	
	topic := mp.config.TopicPrefix + "/" + subtopic
	token := mp.client.Publish(topic, mp.config.QoS, mp.config.Retain, payload)
	if token.Wait() && token.Error() != nil {
		LogInfo("MQTT: Error publishing to %s: %v", topic, token.Error())
	}
}

func (mp *MQTTPublisher) publishFloat(subtopic string, value float64) {
	topic := mp.config.TopicPrefix + "/" + subtopic
	payload := strconv.FormatFloat(value, 'f', 2, 64)
	token := mp.client.Publish(topic, mp.config.QoS, mp.config.Retain, payload)
	if token.Wait() && token.Error() != nil {
		LogInfo("MQTT: Error publishing to %s: %v", topic, token.Error())
	}
}

func (mp *MQTTPublisher) publishInt(subtopic string, value int) {
	topic := mp.config.TopicPrefix + "/" + subtopic
	payload := strconv.Itoa(value)
	token := mp.client.Publish(topic, mp.config.QoS, mp.config.Retain, payload)
	if token.Wait() && token.Error() != nil {
		LogInfo("MQTT: Error publishing to %s: %v", topic, token.Error())
	}
}

func (mp *MQTTPublisher) publishString(subtopic string, value string) {
	topic := mp.config.TopicPrefix + "/" + subtopic
	token := mp.client.Publish(topic, mp.config.QoS, mp.config.Retain, value)
	if token.Wait() && token.Error() != nil {
		LogInfo("MQTT: Error publishing to %s: %v", topic, token.Error())
	}
}

func (mp *MQTTPublisher) publishStatus(status string) {
	mp.publishString("status", status)
}

// Graceful shutdown
func (mp *MQTTPublisher) Shutdown() {
	if mp == nil {
		return
	}
	LogInfo("MQTT: Shutting down publisher...")
	close(mp.shutdown)
}

// Get connection status
func (mp *MQTTPublisher) IsConnected() bool {
	if mp == nil {
		return false
	}
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()
	return mp.connected
}

// Manual publish trigger (useful for testing)
func (e *EnvoyExporter) ForceMQTTPublish() {
	if e.mqttPublisher != nil {
		LogInfo("MQTT: Force publishing metrics...")
		e.mqttPublisher.publishMetrics(e)
	}
}
