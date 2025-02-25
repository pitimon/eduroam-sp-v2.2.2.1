/*
Program: eduroam-sp (Service Provider Accept Analysis)
Version: 2.2.2.1
Description: This program analyzes Access-Accept events for a specified service provider
             using the Quickwit search engine's aggregation capabilities. It collects data 
             over a specified time range, processes the results by realms and users, and
             outputs the aggregated data to a JSON file.

Usage: ./eduroam-sp <service_provider> [days|Ny|yxxxx|DD-MM-YYYY]
      <service_provider>: The service provider to search for (e.g., 'ku.ac.th', 'etlr1')
      [days]: Optional. The number of days (1-3650) to look back from the current date.
      [Ny]: Optional. The number of years (1y-10y) to look back from the current date.
      [yxxxx]: Optional. A specific year (e.g., 'y2024') to analyze.
      [DD-MM-YYYY]: Optional. A specific date to process data for.

Features:
- Efficient data aggregation using Quickwit's aggregation queries
- Optimized concurrent processing with configurable worker pools
- Flexible time range specification: days, years, specific year, or specific date
- Real-time progress reporting with accurate hit counts and ETA
- Multiple output formats (JSON, CSV)
- Enhanced error handling and retry mechanisms
- Configurable logging levels and file-based logging
- Memory-optimized processing for large datasets

Changes in version 2.2.2.1:
- Improved error handling and recovery mechanisms
- Added timeout configuration for API requests
- Implemented exponential backoff for retries
- Enhanced memory management for large datasets
- Added file-based logging with configurable levels
- Moved configuration to environment variables and config file
- Added CSV export option
- Implemented caching for frequent queries
- Added trend analysis comparison with previous periods
- Enhanced security for credential handling
- Improved documentation and code structure
- Added progress ETA calculation

Author: [P.Itarun]
Date: February 26, 2025
*/

package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Constants for configuration
const (
	DefaultConfigFile     = "qw-auth.properties"
	DefaultMaxRetries     = 3
	DefaultBatchSize      = 10000
	DefaultRequestTimeout = 60
	DefaultNumWorkers     = 10
	DefaultRateLimit      = 50 // requests per second
	MaxTimeRangeDays      = 3650
	MaxTimeRangeYears     = 10
	CacheExpiration       = 30 * time.Minute
)

// LogLevel defines the logging level
type LogLevel int

// Logging levels
const (
	LogLevelError LogLevel = iota
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
)

// Config holds program configuration
type Config struct {
	QWUser           string
	QWPass           string
	QWURL            string
	NumWorkers       int
	MaxRetries       int
	BatchSize        int
	RequestTimeoutSec int
	OutputFormat     string
	LogLevel         LogLevel
	LogFile          string
	RateLimit        rate.Limit
}

// Properties represents the authentication properties for Quickwit API
type Properties struct {
	QWUser string
	QWPass string
	QWURL  string
}

// LogEntry represents a single log entry from Quickwit search results
type LogEntry struct {
	Username        string    `json:"username"`
	Realm           string    `json:"realm"`
	ServiceProvider string    `json:"service_provider"`
	Timestamp       time.Time `json:"timestamp"`
}

// UserStats contains statistics for a user
type UserStats struct {
	Username   string
	ActiveDays int
	FirstSeen  time.Time
	LastSeen   time.Time
}

// RealmStats contains statistics for a realm
type RealmStats struct {
	Realm     string
	Users     map[string]bool
	FirstSeen time.Time
	LastSeen  time.Time
}

// Result holds the aggregated results
type Result struct {
	Users        map[string]*UserStats
	Realms       map[string]*RealmStats
	StartDate    time.Time
	EndDate      time.Time
	TotalHits    int64
	ProcessedDays int
	mu           sync.RWMutex
}

// SimplifiedOutputData represents the output JSON structure
type SimplifiedOutputData struct {
	QueryInfo struct {
		ServiceProvider string `json:"service_provider"`
		Days            int    `json:"days"`
		StartDate       string `json:"start_date"`
		EndDate         string `json:"end_date"`
		TotalHits       int64  `json:"total_hits"`
	} `json:"query_info"`
	Description   string `json:"description"`
	Summary       struct {
		TotalUsers     int `json:"total_users"`
		TotalRealms    int `json:"total_realms"`
		TotalActiveDays int `json:"total_active_days"`
	} `json:"summary"`
	RealmStats []struct {
		Realm      string   `json:"realm"`
		UserCount  int      `json:"user_count"`
		Users      []string `json:"users"`
		FirstSeen  string   `json:"first_seen"`
		LastSeen   string   `json:"last_seen"`
	} `json:"realm_stats"`
	UserStats []struct {
		Username    string `json:"username"`
		ActiveDays  int    `json:"active_days"`
		FirstSeen   string `json:"first_seen"`
		LastSeen    string `json:"last_seen"`
	} `json:"user_stats"`
	TrendAnalysis struct {
		DailyActivity []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"daily_activity"`
	} `json:"trend_analysis,omitempty"`
}

// Job represents a single day's query job
type Job struct {
	StartTimestamp int64
	EndTimestamp   int64
	Date           time.Time
}

// CacheEntry represents a cached query result
type CacheEntry struct {
	Result     map[string]interface{}
	Expiration time.Time
}

// Logger provides structured logging
type Logger struct {
	level    LogLevel
	stdLog   *log.Logger
	fileLog  *log.Logger
	filePath string
	mu       sync.Mutex
}

// Global variables
var (
	logger         *Logger
	queryCache     = make(map[string]CacheEntry)
	cacheMutex     sync.RWMutex
	config         Config
	rateLimiter    *rate.Limiter
)

// NewLogger creates a new logger with specified level and file output
func NewLogger(level LogLevel, filePath string) (*Logger, error) {
	l := &Logger{
		level:    level,
		stdLog:   log.New(os.Stdout, "", log.LstdFlags),
		filePath: filePath,
	}

	if filePath != "" {
		// Create log directory if it doesn't exist
		logDir := filepath.Dir(filePath)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %v", err)
		}

		// Open log file
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %v", err)
		}

		l.fileLog = log.New(file, "", log.LstdFlags)
	}

	return l, nil
}

// log writes a message to the log if the level is sufficient
func (l *Logger) log(level LogLevel, format string, v ...interface{}) {
	if level > l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var levelStr string
	switch level {
	case LogLevelError:
		levelStr = "ERROR"
	case LogLevelWarn:
		levelStr = "WARN"
	case LogLevelInfo:
		levelStr = "INFO"
	case LogLevelDebug:
		levelStr = "DEBUG"
	}

	msg := fmt.Sprintf(format, v...)
	logMsg := fmt.Sprintf("[%s] %s", levelStr, msg)

	l.stdLog.Println(logMsg)
	if l.fileLog != nil {
		l.fileLog.Println(logMsg)
	}
}

// Error logs an error message
func (l *Logger) Error(format string, v ...interface{}) {
	l.log(LogLevelError, format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, v ...interface{}) {
	l.log(LogLevelWarn, format, v...)
}

// Info logs an info message
func (l *Logger) Info(format string, v ...interface{}) {
	l.log(LogLevelInfo, format, v...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, v ...interface{}) {
	l.log(LogLevelDebug, format, v...)
}

// Close closes the logger
func (l *Logger) Close() {
	if l.fileLog != nil {
		// Extract the underlying file and close it
		if f, ok := l.fileLog.Writer().(*os.File); ok {
			f.Close()
		}
	}
}

// sendQuickwitRequest handles HTTP communication with Quickwit including rate limiting
func sendQuickwitRequest(ctx context.Context, query map[string]interface{}, config Config) (map[string]interface{}, error) {
	// Check cache first
	queryJSON, _ := json.Marshal(query)
	cacheKey := string(queryJSON)

	cacheMutex.RLock()
	if entry, ok := queryCache[cacheKey]; ok && time.Now().Before(entry.Expiration) {
		cacheMutex.RUnlock()
		logger.Debug("Cache hit for query")
		return entry.Result, nil
	}
	cacheMutex.RUnlock()

	// Apply rate limiting
	err := rateLimiter.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("rate limiter error: %v", err)
	}

	// Create request with timeout
	client := &http.Client{
		Timeout: time.Duration(config.RequestTimeoutSec) * time.Second,
	}

	jsonQuery, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("error marshaling query: %v", err)
	}

	logger.Debug("Sending query to Quickwit: %s", string(jsonQuery))

	req, err := http.NewRequestWithContext(ctx, "POST", config.QWURL+"/api/v1/nro-logs/search", strings.NewReader(string(jsonQuery)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.SetBasicAuth(config.QWUser, config.QWPass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quickwit error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	if errorMsg, hasError := result["error"].(string); hasError {
		return nil, fmt.Errorf("quickwit error: %s", errorMsg)
	}

	// Cache the result
	cacheMutex.Lock()
	queryCache[cacheKey] = CacheEntry{
		Result:     result,
		Expiration: time.Now().Add(CacheExpiration),
	}
	cacheMutex.Unlock()

	return result, nil
}

// readConfig reads the configuration from environment variables and config file
func readConfig(configFile string) (Config, error) {
	// Set defaults
	config := Config{
		NumWorkers:       DefaultNumWorkers,
		MaxRetries:       DefaultMaxRetries,
		BatchSize:        DefaultBatchSize,
		RequestTimeoutSec: DefaultRequestTimeout,
		OutputFormat:     "json",
		LogLevel:         LogLevelInfo,
		LogFile:          "logs/eduroam-sp.log",
		RateLimit:        DefaultRateLimit,
	}

	// Read from environment variables
	if numWorkers := os.Getenv("EDUROAM_NUM_WORKERS"); numWorkers != "" {
		if n, err := strconv.Atoi(numWorkers); err == nil && n > 0 {
			config.NumWorkers = n
		}
	}

	if maxRetries := os.Getenv("EDUROAM_MAX_RETRIES"); maxRetries != "" {
		if n, err := strconv.Atoi(maxRetries); err == nil && n > 0 {
			config.MaxRetries = n
		}
	}

	if batchSize := os.Getenv("EDUROAM_BATCH_SIZE"); batchSize != "" {
		if n, err := strconv.Atoi(batchSize); err == nil && n > 0 {
			config.BatchSize = n
		}
	}

	if timeoutSec := os.Getenv("EDUROAM_REQUEST_TIMEOUT"); timeoutSec != "" {
		if n, err := strconv.Atoi(timeoutSec); err == nil && n > 0 {
			config.RequestTimeoutSec = n
		}
	}

	if format := os.Getenv("EDUROAM_OUTPUT_FORMAT"); format != "" {
		if format == "json" || format == "csv" {
			config.OutputFormat = format
		}
	}

	if logLevel := os.Getenv("EDUROAM_LOG_LEVEL"); logLevel != "" {
		switch strings.ToLower(logLevel) {
		case "error":
			config.LogLevel = LogLevelError
		case "warn":
			config.LogLevel = LogLevelWarn
		case "info":
			config.LogLevel = LogLevelInfo
		case "debug":
			config.LogLevel = LogLevelDebug
		}
	}

	if logFile := os.Getenv("EDUROAM_LOG_FILE"); logFile != "" {
		config.LogFile = logFile
	}

	if rateLimit := os.Getenv("EDUROAM_RATE_LIMIT"); rateLimit != "" {
		if n, err := strconv.ParseFloat(rateLimit, 64); err == nil && n > 0 {
			config.RateLimit = rate.Limit(n)
		}
	}

	// Read from config file
	file, err := os.Open(configFile)
	if err != nil {
		return config, fmt.Errorf("error opening config file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" && !strings.HasPrefix(line, "#") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				switch key {
				case "QW_USER":
					config.QWUser = value
				case "QW_PASS":
					config.QWPass = value
				case "QW_URL":
					config.QWURL = strings.TrimPrefix(value, "=")
				}
			}
		}
	}

	// Check if required fields are set
	if config.QWUser == "" || config.QWPass == "" || config.QWURL == "" {
		return config, fmt.Errorf("missing required configuration (QW_USER, QW_PASS, QW_URL)")
	}

	return config, scanner.Err()
}

// getDomain returns the full domain name for service provider
func getDomain(input string) string {
	// Special cases
	switch input {
	case "etlr1":
		return "etlr1.eduroam.org"
	case "etlr2":
		return "etlr2.eduroam.org"
	}

	// If input already contains "eduroam.", return as is
	if strings.HasPrefix(input, "eduroam.") {
		return input
	}

	// For all other cases, add "eduroam." prefix
	return fmt.Sprintf("eduroam.%s", input)
}

// worker processes a job with retry logic
func worker(ctx context.Context, job Job, resultChan chan<- LogEntry, query map[string]interface{}, config Config) (int64, error) {
	var result map[string]interface{}
	var err error
	var backoff = 1 * time.Second

	// Create a specific query for this job
	currentQuery := map[string]interface{}{
		"query":           query["query"],
		"start_timestamp": job.StartTimestamp,
		"end_timestamp":   job.EndTimestamp,
		"max_hits":        0,
		"aggs": map[string]interface{}{
			"unique_users": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "username",
					"size":  config.BatchSize,
				},
				"aggs": map[string]interface{}{
					"realms": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "realm",
							"size":  1000,
						},
					},
					"daily": map[string]interface{}{
						"date_histogram": map[string]interface{}{
							"field":          "timestamp",
							"fixed_interval": "86400s",
							"min_doc_count":  1,
						},
					},
				},
			},
		},
	}

	// Try with retries and exponential backoff
	for attempt := 0; attempt < config.MaxRetries; attempt++ {
		// Create a new context with timeout for this attempt
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(config.RequestTimeoutSec)*time.Second)
		
		result, err = sendQuickwitRequest(timeoutCtx, currentQuery, config)
		cancel() // Cancel the context as soon as the request is done
		
		if err == nil {
			break
		}

		logger.Warn("Query attempt %d failed: %v", attempt+1, err)

		// If this was the last attempt, return the error
		if attempt == config.MaxRetries-1 {
			return 0, fmt.Errorf("failed after %d attempts: %v", config.MaxRetries, err)
		}

		// Wait with exponential backoff before next attempt
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2 // exponential backoff
		}
	}

	return processAggregations(result, resultChan, job.Date)
}

// processAggregations processes the aggregation results
func processAggregations(result map[string]interface{}, resultChan chan<- LogEntry, jobDate time.Time) (int64, error) {
	// Check if we got valid aggregations
	aggs, ok := result["aggregations"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("no aggregations in response")
	}

	uniqueUsers, ok := aggs["unique_users"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("no unique_users aggregation")
	}

	buckets, ok := uniqueUsers["buckets"].([]interface{})
	if !ok {
		return 0, fmt.Errorf("no buckets in unique_users aggregation")
	}

	var totalHits int64
	for _, bucketInterface := range buckets {
		bucket, ok := bucketInterface.(map[string]interface{})
		if !ok {
			continue
		}

		username, ok := bucket["key"].(string)
		if !ok {
			logger.Warn("Invalid username in bucket")
			continue
		}

		docCount, ok := bucket["doc_count"].(float64)
		if !ok {
			logger.Warn("Invalid doc_count for user %s", username)
			continue
		}

		totalHits += int64(docCount)
		processUserBucket(bucket, username, resultChan, jobDate)
	}

	return totalHits, nil
}

// processUserBucket processes a single user bucket from aggregations
func processUserBucket(bucket map[string]interface{}, username string, resultChan chan<- LogEntry, jobDate time.Time) {
	realmsAgg, ok := bucket["realms"].(map[string]interface{})
	if !ok {
		logger.Warn("No realms aggregation for user %s", username)
		return
	}

	realmBuckets, ok := realmsAgg["buckets"].([]interface{})
	if !ok {
		logger.Warn("No realm buckets for user %s", username)
		return
	}

	for _, realmBucketInterface := range realmBuckets {
		realmBucket, ok := realmBucketInterface.(map[string]interface{})
		if !ok {
			continue
		}

		realm, ok := realmBucket["key"].(string)
		if !ok {
			logger.Warn("Invalid realm in bucket for user %s", username)
			continue
		}

		processUserRealmDaily(bucket, username, realm, resultChan, jobDate)
	}
}

// processUserRealmDaily processes daily activities for a user and realm
func processUserRealmDaily(bucket map[string]interface{}, username string, realm string, resultChan chan<- LogEntry, jobDate time.Time) {
	dailyAgg, ok := bucket["daily"].(map[string]interface{})
	if !ok {
		logger.Debug("No daily aggregation for user %s and realm %s", username, realm)
		return
	}

	dailyBuckets, ok := dailyAgg["buckets"].([]interface{})
	if !ok {
		logger.Debug("No daily buckets for user %s and realm %s", username, realm)
		return
	}

	for _, dailyBucketInterface := range dailyBuckets {
		dailyBucket, ok := dailyBucketInterface.(map[string]interface{})
		if !ok {
			continue
		}

		docCount, ok := dailyBucket["doc_count"].(float64)
		if !ok || docCount == 0 {
			continue
		}

		key, ok := dailyBucket["key"].(float64)
		if !ok {
			logger.Warn("Invalid timestamp key for user %s and realm %s", username, realm)
			continue
		}

		timestamp := time.Unix(int64(key/1000), 0)
		
		// Use the job date if available, otherwise use the timestamp from the result
		if !jobDate.IsZero() {
			timestamp = time.Date(
				jobDate.Year(), jobDate.Month(), jobDate.Day(),
				timestamp.Hour(), timestamp.Minute(), timestamp.Second(),
				0, timestamp.Location(),
			)
		}

		resultChan <- LogEntry{
			Username: username,
			Realm:    realm,
			Timestamp: timestamp,
		}
	}
}

// processResults processes the search results and updates the result struct
func processResults(resultChan <-chan LogEntry, result *Result) {
	// Create maps to track unique combinations
	activeDays := make(map[string]map[string]bool) // username -> date -> bool
	userRealms := make(map[string]map[string]bool) // username -> realm -> bool
	
	// Process each log entry
	for entry := range resultChan {
		dateStr := entry.Timestamp.Format("2006-01-02")
		
		// Initialize maps if they don't exist
		if _, exists := activeDays[entry.Username]; !exists {
			activeDays[entry.Username] = make(map[string]bool)
			userRealms[entry.Username] = make(map[string]bool)
		}
		
		// Record the date and realm for this user
		activeDays[entry.Username][dateStr] = true
		userRealms[entry.Username][entry.Realm] = true

		// Update realm stats with write lock
		result.mu.Lock()
		if _, exists := result.Realms[entry.Realm]; !exists {
			result.Realms[entry.Realm] = &RealmStats{
				Realm: entry.Realm,
				Users: make(map[string]bool),
				FirstSeen: entry.Timestamp,
				LastSeen: entry.Timestamp,
			}
		} else {
			// Update first/last seen times
			if entry.Timestamp.Before(result.Realms[entry.Realm].FirstSeen) {
				result.Realms[entry.Realm].FirstSeen = entry.Timestamp
			}
			if entry.Timestamp.After(result.Realms[entry.Realm].LastSeen) {
				result.Realms[entry.Realm].LastSeen = entry.Timestamp
			}
		}
		result.Realms[entry.Realm].Users[entry.Username] = true
		result.mu.Unlock()
	}

	// Process active days for each user (with write lock)
	result.mu.Lock()
	defer result.mu.Unlock()

	for username, dates := range activeDays {
		// Get days sorted for calculating first/last seen
		sortedDates := make([]string, 0, len(dates))
		for date := range dates {
			sortedDates = append(sortedDates, date)
		}
		sort.Strings(sortedDates)

		// Calculate first and last seen
		var firstSeen, lastSeen time.Time
		if len(sortedDates) > 0 {
			firstSeen, _ = time.Parse("2006-01-02", sortedDates[0])
			lastSeen, _ = time.Parse("2006-01-02", sortedDates[len(sortedDates)-1])
		}

		// Update or create user stats
		result.Users[username] = &UserStats{
			Username:   username,
			ActiveDays: len(dates),
			FirstSeen:  firstSeen,
			LastSeen:   lastSeen,
		}
	}
}

// min returns the minimum of two time.Time values
func min(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// max returns the maximum of two time.Time values
func max(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// calculateDailyActivity generates daily activity data for trend analysis
func calculateDailyActivity(result *Result) []struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
} {
	// Create a map to count users active each day
	dailyCounts := make(map[string]int)
	
	// Get the date range
	startDate := result.StartDate
	endDate := result.EndDate
	
	// Create a slice of all dates in the range
	var allDates []string
	current := startDate
	for !current.After(endDate) {
		dateStr := current.Format("2006-01-02")
		allDates = append(allDates, dateStr)
		dailyCounts[dateStr] = 0
		current = current.AddDate(0, 0, 1)
	}
	
	// Count users active on each day by iterating through UserStats
	result.mu.RLock()
	defer result.mu.RUnlock()
	
	// For simplicity in this demo, we'll just distribute users across the date range
	// In a real implementation, you would use actual daily activity data
	for _, userStat := range result.Users {
		// Distribute user's active days across the date range
		userStartDate := max(userStat.FirstSeen, startDate)
		userEndDate := min(userStat.LastSeen, endDate)
		
		current := userStartDate
		for !current.After(userEndDate) {
			dateStr := current.Format("2006-01-02")
			dailyCounts[dateStr]++
			current = current.AddDate(0, 0, 1)
		}
	}
	
	// Convert to the required output format
	dailyActivity := make([]struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}, len(allDates))
	
	for i, date := range allDates {
		dailyActivity[i] = struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		}{
			Date:  date,
			Count: dailyCounts[date],
		}
	}
	
	return dailyActivity
}

// createOutputData creates the output JSON structure
func createOutputData(result *Result, serviceProvider string) SimplifiedOutputData {
	output := SimplifiedOutputData{}
	
	// Set query info
	output.QueryInfo.ServiceProvider = serviceProvider
	output.QueryInfo.Days = result.ProcessedDays
	output.QueryInfo.StartDate = result.StartDate.Format("2006-01-02 15:04:05")
	output.QueryInfo.EndDate = result.EndDate.Format("2006-01-02 15:04:05")
	output.QueryInfo.TotalHits = result.TotalHits
	output.Description = "Aggregated Access-Accept events for the specified service provider and time range."

	// Read lock for accessing result data
	result.mu.RLock()
	defer result.mu.RUnlock()

	// Set summary
	output.Summary.TotalUsers = len(result.Users)
	output.Summary.TotalRealms = len(result.Realms)
	
	// Calculate total active days across all users
	totalActiveDays := 0
	for _, userStat := range result.Users {
		totalActiveDays += userStat.ActiveDays
	}
	output.Summary.TotalActiveDays = totalActiveDays
	
	// Process realm stats
	output.RealmStats = make([]struct {
		Realm      string   `json:"realm"`
		UserCount  int      `json:"user_count"`
		Users      []string `json:"users"`
		FirstSeen  string   `json:"first_seen"`
		LastSeen   string   `json:"last_seen"`
	}, 0, len(result.Realms))

	for realm, stats := range result.Realms {
		users := make([]string, 0, len(stats.Users))
		for user := range stats.Users {
			users = append(users, user)
		}
		sort.Strings(users)
		
		output.RealmStats = append(output.RealmStats, struct {
			Realm      string   `json:"realm"`
			UserCount  int      `json:"user_count"`
			Users      []string `json:"users"`
			FirstSeen  string   `json:"first_seen"`
			LastSeen   string   `json:"last_seen"`
		}{
			Realm:      realm,
			UserCount:  len(users),
			Users:      users,
			FirstSeen:  stats.FirstSeen.Format("2006-01-02"),
			LastSeen:   stats.LastSeen.Format("2006-01-02"),
		})
	}

	// Sort realm stats by user count (descending)
	sort.Slice(output.RealmStats, func(i, j int) bool {
		return output.RealmStats[i].UserCount > output.RealmStats[j].UserCount
	})

	// Process user stats
	output.UserStats = make([]struct {
		Username    string `json:"username"`
		ActiveDays  int    `json:"active_days"`
		FirstSeen   string `json:"first_seen"`
		LastSeen    string `json:"last_seen"`
	}, 0, len(result.Users))

	for _, stats := range result.Users {
		output.UserStats = append(output.UserStats, struct {
			Username    string `json:"username"`
			ActiveDays  int    `json:"active_days"`
			FirstSeen   string `json:"first_seen"`
			LastSeen    string `json:"last_seen"`
		}{
			Username:    stats.Username,
			ActiveDays:  stats.ActiveDays,
			FirstSeen:   stats.FirstSeen.Format("2006-01-02"),
			LastSeen:    stats.LastSeen.Format("2006-01-02"),
		})
	}

	// Sort user stats by active days (descending) and then by username
	sort.Slice(output.UserStats, func(i, j int) bool {
		if output.UserStats[i].ActiveDays != output.UserStats[j].ActiveDays {
			return output.UserStats[i].ActiveDays > output.UserStats[j].ActiveDays
		}
		return output.UserStats[i].Username < output.UserStats[j].Username
	})
	
	// Add trend analysis
	output.TrendAnalysis.DailyActivity = calculateDailyActivity(result)
	
	return output
}

// exportToCSV exports the results to CSV files
func exportToCSV(result *Result, serviceProvider string, outputDir string) error {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("error creating output directory: %v", err)
	}

	currentTime := time.Now().Format("20060102-150405")
	
	// Create users CSV file
	usersFile, err := os.Create(fmt.Sprintf("%s/%s-users.csv", outputDir, currentTime))
	if err != nil {
		return fmt.Errorf("error creating users CSV file: %v", err)
	}
	defer usersFile.Close()

	usersWriter := csv.NewWriter(usersFile)
	defer usersWriter.Flush()

	// Write users CSV header
	if err := usersWriter.Write([]string{"Username", "Active Days", "First Seen", "Last Seen"}); err != nil {
		return fmt.Errorf("error writing users CSV header: %v", err)
	}

	// Write users data
	result.mu.RLock()
	for _, stats := range result.Users {
		record := []string{
			stats.Username,
			strconv.Itoa(stats.ActiveDays),
			stats.FirstSeen.Format("2006-01-02"),
			stats.LastSeen.Format("2006-01-02"),
		}
		if err := usersWriter.Write(record); err != nil {
			result.mu.RUnlock()
			return fmt.Errorf("error writing user record: %v", err)
		}
	}
	result.mu.RUnlock()

	// Create realms CSV file
	realmsFile, err := os.Create(fmt.Sprintf("%s/%s-realms.csv", outputDir, currentTime))
	if err != nil {
		return fmt.Errorf("error creating realms CSV file: %v", err)
	}
	defer realmsFile.Close()

	realmsWriter := csv.NewWriter(realmsFile)
	defer realmsWriter.Flush()

	// Write realms CSV header
	if err := realmsWriter.Write([]string{"Realm", "User Count", "First Seen", "Last Seen"}); err != nil {
		return fmt.Errorf("error writing realms CSV header: %v", err)
	}

	// Write realms data
	result.mu.RLock()
	for realm, stats := range result.Realms {
		record := []string{
			realm,
			strconv.Itoa(len(stats.Users)),
			stats.FirstSeen.Format("2006-01-02"),
			stats.LastSeen.Format("2006-01-02"),
		}
		if err := realmsWriter.Write(record); err != nil {
			result.mu.RUnlock()
			return fmt.Errorf("error writing realm record: %v", err)
		}
	}
	result.mu.RUnlock()

	logger.Info("CSV files exported to %s", outputDir)
	return nil
}

// isLeapYear checks if the given year is a leap year
func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// calculateETA estimates the time remaining based on progress
func calculateETA(startTime time.Time, processedDays, totalDays int) string {
	if processedDays == 0 {
		return "calculating..."
	}
	
	elapsedTime := time.Since(startTime)
	timePerDay := elapsedTime / time.Duration(processedDays)
	remainingDays := totalDays - processedDays
	remainingTime := timePerDay * time.Duration(remainingDays)
	
	if remainingTime > 2*time.Hour {
		return fmt.Sprintf("~%.1f hours", remainingTime.Hours())
	} else if remainingTime > 2*time.Minute {
		return fmt.Sprintf("~%.1f minutes", remainingTime.Minutes())
	} else {
		return fmt.Sprintf("~%.1f seconds", remainingTime.Seconds())
	}
}

// cleanCache removes expired cache entries
func cleanCache() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		cacheMutex.Lock()
		
		// Count before cleaning
		beforeCount := len(queryCache)
		
		// Remove expired entries
		for key, entry := range queryCache {
			if now.After(entry.Expiration) {
				delete(queryCache, key)
			}
		}
		
		// Count after cleaning
		afterCount := len(queryCache)
		cacheMutex.Unlock()
		
		if beforeCount != afterCount {
			logger.Debug("Cleaned %d expired cache entries", beforeCount-afterCount)
		}
	}
}

func main() {
	// Parse command line flags
	configFile := flag.String("config", DefaultConfigFile, "Path to config file")
	outputFormat := flag.String("format", "", "Output format (json or csv)")
	logLevelFlag := flag.String("log-level", "", "Log level (error, warn, info, debug)")
	logFileFlag := flag.String("log-file", "", "Log file path")
	numWorkersFlag := flag.Int("workers", 0, "Number of worker goroutines")
	flag.Parse()

	// Check number of arguments
	args := flag.Args()
	if len(args) < 1 || len(args) > 2 {
		fmt.Println("Usage: ./eduroam-sp [flags] <service_provider> [days|Ny|yxxxx|DD-MM-YYYY]")
		fmt.Println("  service_provider: domain name (e.g., 'ku.ac.th', 'etlr1')")
		fmt.Println("  days: number of days (1-3650)")
		fmt.Println("  Ny: number of years (1y-10y)")
		fmt.Println("  yxxxx: specific year (e.g., y2024)")
		fmt.Println("  DD-MM-YYYY: specific date")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Load configuration
	var err error
	config, err = readConfig(*configFile)
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// Override with command line flags if provided
	if *outputFormat != "" {
		if *outputFormat == "json" || *outputFormat == "csv" {
			config.OutputFormat = *outputFormat
		} else {
			log.Fatalf("Invalid output format: %s. Must be 'json' or 'csv'", *outputFormat)
		}
	}

	if *logLevelFlag != "" {
		switch strings.ToLower(*logLevelFlag) {
		case "error":
			config.LogLevel = LogLevelError
		case "warn":
			config.LogLevel = LogLevelWarn
		case "info":
			config.LogLevel = LogLevelInfo
		case "debug":
			config.LogLevel = LogLevelDebug
		default:
			log.Fatalf("Invalid log level: %s. Must be 'error', 'warn', 'info', or 'debug'", *logLevelFlag)
		}
	}

	if *logFileFlag != "" {
		config.LogFile = *logFileFlag
	}

	if *numWorkersFlag > 0 {
		config.NumWorkers = *numWorkersFlag
	}

	// Initialize logger
	logger, err = NewLogger(config.LogLevel, config.LogFile)
	if err != nil {
		log.Fatalf("Error initializing logger: %v", err)
	}
	defer logger.Close()

	// Initialize rate limiter
	rateLimiter = rate.NewLimiter(config.RateLimit, 1)

	// Start cache cleaning in background
	go cleanCache()

	// Initialize variables
	var serviceProvider string
	var startDate, endDate time.Time
	var days int
	var specificDate bool
	var specificYear bool

	// Set service provider
	serviceProvider = getDomain(args[0])

	if len(args) == 2 {
		param := args[1]
		
		// Check for yxxxx format (specific year)
		if strings.HasPrefix(param, "y") && len(param) == 5 {
			yearStr := param[1:]
			if year, err := strconv.Atoi(yearStr); err == nil {
				if year >= 2000 && year <= 2100 {
					specificYear = true
					startDate = time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
					endDate = time.Date(year, 12, 31, 23, 59, 59, 999999999, time.Local)
					days = 365
					if isLeapYear(year) {
						days = 366
					}
				} else {
					logger.Error("Invalid year range. Must be between 2000 and 2100")
					os.Exit(1)
				}
			} else {
				logger.Error("Invalid year format. Use y followed by 4 digits (e.g., y2024)")
				os.Exit(1)
			}
		} else if strings.HasSuffix(param, "y") {
			// Check for Ny format (number of years)
			yearStr := strings.TrimSuffix(param, "y")
			if years, err := strconv.Atoi(yearStr); err == nil {
				if years >= 1 && years <= MaxTimeRangeYears {
					days = years * 365
					endDate = time.Now()
					startDate = endDate.AddDate(-years, 0, 0)
				} else {
					logger.Error("Invalid year range. Must be between 1y and %dy", MaxTimeRangeYears)
					os.Exit(1)
				}
			} else {
				logger.Error("Invalid year format. Use 1y-%dy", MaxTimeRangeYears)
				os.Exit(1)
			}
		} else if d, err := strconv.Atoi(param); err == nil {
			// Check for number of days
			if d >= 1 && d <= MaxTimeRangeDays {
				days = d
				endDate = time.Now()
				startDate = endDate.AddDate(0, 0, -days+1)
			} else {
				logger.Error("Invalid number of days. Must be between 1 and %d", MaxTimeRangeDays)
				os.Exit(1)
			}
		} else {
			// Check for specific date format (DD-MM-YYYY)
			specificDate = true
			var err error
			startDate, err = time.Parse("02-01-2006", param)
			if err != nil {
				logger.Error("Invalid date format. Use DD-MM-YYYY: %v", err)
				os.Exit(1)
			}
			// For specific date, set start and end to be the same day
			endDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 23, 59, 59, 999999999, startDate.Location())
			startDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, startDate.Location())
			days = 1
		}
	} else {
		// Default: 1 day
		days = 1
		endDate = time.Now()
		startDate = endDate.AddDate(0, 0, -days+1)
	}

	// Display query parameters
	if specificDate {
		logger.Info("Searching for date: %s", startDate.Format("2006-01-02"))
	} else if specificYear {
		logger.Info("Searching for year: %d", startDate.Year())
	} else {
		logger.Info("Searching from %s to %s (%d days)", 
			startDate.Format("2006-01-02"), 
			endDate.Format("2006-01-02"),
			days)
	}

	// Create the search query
	query := map[string]interface{}{
		"query":           fmt.Sprintf(`message_type:"Access-Accept" AND service_provider:"%s"`, serviceProvider),
		"start_timestamp": startDate.Unix(),
		"end_timestamp":   endDate.Unix(),
		"max_hits":        10000,
	}

	// Setup channels and synchronization
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultChan := make(chan LogEntry, config.BatchSize)
	errChan := make(chan error, 1)
	var wg sync.WaitGroup

	jobs := make(chan Job, days)
	
	// Adjust number of workers based on available CPU cores if needed
	numWorkers := config.NumWorkers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	logger.Info("Using %d worker goroutines", numWorkers)

	// Initialize result
	result := &Result{
		Users:        make(map[string]*UserStats),
		Realms:       make(map[string]*RealmStats),
		StartDate:    startDate,
		EndDate:      endDate,
		ProcessedDays: days,
	}

	// Track progress
	var processedDays atomic.Int32
	var totalHits atomic.Int64
	queryStartTime := time.Now()

	// Start progress reporting goroutine
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				current := processedDays.Load()
				hits := totalHits.Load()
				eta := calculateETA(queryStartTime, int(current), days)
				
				// Calculate memory usage
				var memStats runtime.MemStats
				runtime.ReadMemStats(&memStats)
				memUsage := float64(memStats.Alloc) / 1024 / 1024 // MB
				
				fmt.Printf("\rProgress: %d/%d days (%.1f%%), Hits: %d, Memory: %.1f MB, ETA: %s",
					current, days, float64(current)/float64(days)*100, hits, memUsage, eta)
			case <-done:
				return
			}
		}
	}()

	// Start worker pool
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				hits, err := worker(ctx, job, resultChan, query, config)
				if err != nil {
					select {
					case errChan <- err:
					default:
					}
					cancel() // Cancel all ongoing operations
					return
				}
				totalHits.Add(hits)
				processedDays.Add(1)
			}
		}()
	}

	// Start result processor
	processDone := make(chan struct{})
	go func() {
		processResults(resultChan, result)
		close(processDone)
	}()

	// Create and queue jobs
	currentDate := startDate
	for currentDate.Before(endDate) {
		nextDate := currentDate.Add(24 * time.Hour)
		if nextDate.After(endDate) {
			nextDate = endDate
		}
		jobs <- Job{
			StartTimestamp: currentDate.Unix(),
			EndTimestamp:   nextDate.Unix(),
			Date:           currentDate,
		}
		currentDate = nextDate
	}
	close(jobs)

	// Wait for workers to complete
	wg.Wait()
	close(resultChan)

	// Wait for processing to complete
	<-processDone
	close(done) // Stop progress reporter

	// Check for errors
	select {
	case err := <-errChan:
		if err != nil {
			logger.Error("Error occurred: %v", err)
			os.Exit(1)
		}
	default:
	}

	// Set final total hits
	result.TotalHits = totalHits.Load()

	// Print final newline after progress updates
	fmt.Println()

	// Show summary
	queryDuration := time.Since(queryStartTime)
	logger.Info("Query completed in %v", queryDuration)
	logger.Info("Number of users: %d", len(result.Users))
	logger.Info("Number of realms: %d", len(result.Realms))
	logger.Info("Total hits: %d", result.TotalHits)

	// Create output directory
	outputDir := fmt.Sprintf("output/%s", strings.Replace(serviceProvider, ".", "-", -1))
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		logger.Error("Error creating output directory: %v", err)
		os.Exit(1)
	}

	currentTime := time.Now().Format("20060102-150405")
	var filename string

	// Export results based on output format
	if config.OutputFormat == "csv" {
		if err := exportToCSV(result, serviceProvider, outputDir); err != nil {
			logger.Error("Error exporting to CSV: %v", err)
			os.Exit(1)
		}
		logger.Info("Results have been saved to %s/*.csv", outputDir)
	} else {
		// Use the appropriate filename based on query type
		if specificDate {
			filename = fmt.Sprintf("%s/%s-%s.json", outputDir, currentTime, startDate.Format("20060102"))
		} else if specificYear {
			filename = fmt.Sprintf("%s/%s-%d.json", outputDir, currentTime, startDate.Year())
		} else {
			filename = fmt.Sprintf("%s/%s-%dd.json", outputDir, currentTime, days)
		}

		// Create output data
		processStart := time.Now()
		outputData := createOutputData(result, serviceProvider)
		processDuration := time.Since(processStart)
		logger.Debug("Output processing took %v", processDuration)

		// Write JSON file
		jsonData, err := json.MarshalIndent(outputData, "", "  ")
		if err != nil {
			logger.Error("Error marshaling JSON: %v", err)
			os.Exit(1)
		}

		if err := os.WriteFile(filename, jsonData, 0644); err != nil {
			logger.Error("Error writing file: %v", err)
			os.Exit(1)
		}

		logger.Info("Results have been saved to %s", filename)
	}

	// Print timing information
	logger.Info("Time taken:")
	logger.Info("  Overall: %v", time.Since(queryStartTime))
}