// daily_production.go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Daily production data structures
type HourlyData struct {
	Hour         int     `json:"hour"`
	Production   float64 `json:"production"`   // Wh for this hour
	Power        float64 `json:"power"`        // Average watts for this hour
	Timestamp    int64   `json:"timestamp"`
	SampleCount  int     `json:"sample_count"`
}

type DailyProduction struct {
	Date         string        `json:"date"`          // YYYY-MM-DD format
	HourlyData   []HourlyData  `json:"hourly_data"`
	TotalWh      float64       `json:"total_wh"`
	PeakWatts    float64       `json:"peak_watts"`
	PeakHour     int           `json:"peak_hour"`
	FirstSample  int64         `json:"first_sample"`
	LastSample   int64         `json:"last_sample"`
	SampleCount  int           `json:"sample_count"`
}

type ProductionHistory struct {
	Days         map[string]*DailyProduction `json:"days"`
	LastCleanup  int64                       `json:"last_cleanup"`
	mutex        sync.RWMutex
}

// Production tracker for the exporter
type ProductionTracker struct {
	history     *ProductionHistory
	dataFile    string
	saveMutex   sync.Mutex
	lastSave    int64
}

// Initialize production tracking
func (e *EnvoyExporter) initProductionTracking() {
	if e.productionTracker != nil {
		return
	}

	dataFile := filepath.Join(e.config.WebDir, "production_history.json")
	
	tracker := &ProductionTracker{
		dataFile: dataFile,
		history: &ProductionHistory{
			Days: make(map[string]*DailyProduction),
		},
	}

	// Load existing data
	tracker.loadHistory()

	// Start tracking goroutine
	go tracker.trackingLoop(e)

	e.productionTracker = tracker
	log.Printf("Production tracking initialized with data file: %s", dataFile)
}

func (pt *ProductionTracker) loadHistory() {
	pt.history.mutex.Lock()
	defer pt.history.mutex.Unlock()

	data, err := os.ReadFile(pt.dataFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error reading production history: %v", err)
		}
		return
	}

	var loadedHistory ProductionHistory
	if err := json.Unmarshal(data, &loadedHistory); err != nil {
		log.Printf("Error parsing production history: %v", err)
		return
	}

	pt.history.Days = loadedHistory.Days
	pt.history.LastCleanup = loadedHistory.LastCleanup

	// Initialize hourly data slices if missing
	for _, day := range pt.history.Days {
		if day.HourlyData == nil {
			day.HourlyData = make([]HourlyData, 24)
			for i := range day.HourlyData {
				day.HourlyData[i].Hour = i
			}
		}
	}

	log.Printf("Loaded production history for %d days", len(pt.history.Days))
}

func (pt *ProductionTracker) saveHistory() {
	pt.saveMutex.Lock()
	defer pt.saveMutex.Unlock()

	pt.history.mutex.RLock()
	data, err := json.MarshalIndent(pt.history, "", "  ")
	pt.history.mutex.RUnlock()

	if err != nil {
		log.Printf("Error marshaling production history: %v", err)
		return
	}

	// Write to temporary file first, then rename (atomic operation)
	tempFile := pt.dataFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		log.Printf("Error writing production history temp file: %v", err)
		return
	}

	if err := os.Rename(tempFile, pt.dataFile); err != nil {
		log.Printf("Error renaming production history file: %v", err)
		os.Remove(tempFile)
		return
	}

	pt.lastSave = time.Now().Unix()
}

func (pt *ProductionTracker) trackingLoop(exporter *EnvoyExporter) {
	ticker := time.NewTicker(5 * time.Minute) // Sample every 5 minutes
	defer ticker.Stop()

	saveTicker := time.NewTicker(15 * time.Minute) // Save every 15 minutes
	defer saveTicker.Stop()

	cleanupTicker := time.NewTicker(24 * time.Hour) // Cleanup once per day
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ticker.C:
			pt.recordCurrentProduction(exporter)

		case <-saveTicker.C:
			pt.saveHistory()

		case <-cleanupTicker.C:
			pt.cleanupOldData()
		}
	}
}

func (pt *ProductionTracker) recordCurrentProduction(exporter *EnvoyExporter) {
	now := time.Now()
	dateStr := now.Format("2006-01-02")
	hour := now.Hour()

	// Get current monitor data
	exporter.monitorMutex.RLock()
	monitorData := exporter.lastMonitorData
	exporter.monitorMutex.RUnlock()

	if monitorData.Production.CurrentWatts == 0 && monitorData.Production.TodayWh == 0 {
		return // No valid data
	}

	pt.history.mutex.Lock()
	defer pt.history.mutex.Unlock()

	// Ensure day exists
	if pt.history.Days[dateStr] == nil {
		pt.history.Days[dateStr] = &DailyProduction{
			Date:       dateStr,
			HourlyData: make([]HourlyData, 24),
		}
		// Initialize hourly data
		for i := range pt.history.Days[dateStr].HourlyData {
			pt.history.Days[dateStr].HourlyData[i].Hour = i
		}
	}

	day := pt.history.Days[dateStr]
	hourData := &day.HourlyData[hour]

	// Update hourly data
	if hourData.SampleCount == 0 {
		hourData.Power = monitorData.Production.CurrentWatts
		hourData.Timestamp = now.Unix()
	} else {
		// Running average
		hourData.Power = (hourData.Power*float64(hourData.SampleCount) + monitorData.Production.CurrentWatts) / float64(hourData.SampleCount+1)
	}
	
	hourData.SampleCount++
	hourData.Timestamp = now.Unix()

	// Estimate hourly production (5-minute sample * 12 = hour estimate)
	if hourData.SampleCount == 1 {
		hourData.Production = monitorData.Production.CurrentWatts * 5 / 60 // 5 minutes worth
	} else {
		// Update based on average power
		hourData.Production = hourData.Power * 1.0 // 1 hour worth of average power
	}

	// Update daily totals
	day.TotalWh = monitorData.Production.TodayWh
	if monitorData.Production.CurrentWatts > day.PeakWatts {
		day.PeakWatts = monitorData.Production.CurrentWatts
		day.PeakHour = hour
	}
	if day.FirstSample == 0 {
		day.FirstSample = now.Unix()
	}
	day.LastSample = now.Unix()
	day.SampleCount++
}

func (pt *ProductionTracker) cleanupOldData() {
	pt.history.mutex.Lock()
	defer pt.history.mutex.Unlock()

	cutoff := time.Now().AddDate(0, 0, -30) // Keep 30 days
	cutoffStr := cutoff.Format("2006-01-02")

	for dateStr := range pt.history.Days {
		if dateStr < cutoffStr {
			delete(pt.history.Days, dateStr)
		}
	}

	pt.history.LastCleanup = time.Now().Unix()
	log.Printf("Cleaned up production data older than %s", cutoffStr)
}

// API endpoint for daily production data
func (e *EnvoyExporter) serveDailyProductionAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if e.productionTracker == nil {
		http.Error(w, "Production tracking not initialized", http.StatusServiceUnavailable)
		return
	}

	// Get query parameters
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	}

	previousDate := r.URL.Query().Get("previous")
	if previousDate == "" {
		// Default to yesterday
		yesterday := time.Now().AddDate(0, 0, -1)
		previousDate = yesterday.Format("2006-01-02")
	}

	pt := e.productionTracker
	pt.history.mutex.RLock()
	defer pt.history.mutex.RUnlock()

	response := map[string]interface{}{
		"date": dateStr,
		"previous_date": previousDate,
		"current_day": pt.history.Days[dateStr],
		"previous_day": pt.history.Days[previousDate],
		"available_dates": pt.getAvailableDates(),
	}

	json.NewEncoder(w).Encode(response)
}

func (pt *ProductionTracker) getAvailableDates() []string {
	dates := make([]string, 0, len(pt.history.Days))
	for date := range pt.history.Days {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	return dates
}

// Add to the main exporter struct
func (e *EnvoyExporter) addProductionTracker() {
	// Add this field to EnvoyExporter struct if not already present
	// productionTracker *ProductionTracker
}

// Update the main function to include the new endpoint and initialization
func (e *EnvoyExporter) setupProductionEndpoints() {
	// Initialize production tracking
	e.initProductionTracking()

	// Add the API endpoint
	http.HandleFunc("/api/daily-production", e.serveDailyProductionAPI)
	
	log.Printf("Daily production tracking and API endpoints configured")
}

// Helper function to format production data for frontend
func formatProductionForAPI(day *DailyProduction) map[string]interface{} {
	if day == nil {
		return nil
	}

	return map[string]interface{}{
		"date":         day.Date,
		"hourly_data":  day.HourlyData,
		"total_wh":     day.TotalWh,
		"peak_watts":   day.PeakWatts,
		"peak_hour":    day.PeakHour,
		"sample_count": day.SampleCount,
	}
}
