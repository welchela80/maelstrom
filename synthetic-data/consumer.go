// consumer.go
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// =============================================================================
// ERM Common Log Format (IDD v1.1.4 §Telemetry and Logging)
// =============================================================================

const appName = "sensor-consumer"

type logEntry struct {
	Time  string `json:"time"`
	App   string `json:"app"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

func logInfo(msg string)  { writeLog("info", msg) }
func logError(msg string) { writeLog("error", msg) }
func logFatal(msg string) { writeLog("fatal", msg); os.Exit(1) }

func writeLog(level, msg string) {
	entry := logEntry{
		Time:  time.Now().UTC().Format(time.RFC3339),
		App:   appName,
		Level: level,
		Msg:   msg,
	}
	data, _ := json.Marshal(entry)
	fmt.Println(string(data))
}

// =============================================================================
// Domain types
// =============================================================================

// SensorReading matches PDTSensorValue published by the ERM test driver.
// Exchange:    erm.ex.values
// Routing key: {SystemName}.{MachineName}.{SensorName}
type SensorReading struct {
	SystemName  string `json:"SystemName"`
	MachineName string `json:"MachineName"`
	SensorName  string `json:"SensorName"`
	Value       string `json:"Value"`
	SensorID    string `json:"SensorID"`
}

// SensorKey returns the composite lookup key matching the operational limits
// CSV format: "MachineName:SensorName"
func (r SensorReading) SensorKey() string {
	return fmt.Sprintf("%s:%s", r.MachineName, r.SensorName)
}

type OperationalLimit struct {
	SensorName      string
	OperationalHigh float64
	OperationalLow  float64
}

type SensorAggregate struct {
	Sum      float64
	Count    int
	SensorID string // preserved from incoming message for outcome reporting
}

type MachineStatus struct {
	TotalSensors   int
	GoodSensors    int
	WarningSensors int
	OfflineSensors int
	AboveSensors   int
	BelowSensors   int
	AvgPercentage  float64
}

// MachineStatusRecord tracks the last reported status per machine.
// Retained for future use (e.g. alerting on change).
type MachineStatusRecord struct {
	Status    string
	UpdatedAt time.Time
}

var operationalLimits map[string]OperationalLimit
var sensorAggregates map[string]*SensorAggregate
var aggregateMutex sync.Mutex
var lastReportTime time.Time

// machineStatusHistory tracks the previous status of each machine.
// Retained for future alerting use.
var machineStatusHistory map[string]*MachineStatusRecord
var statusHistoryMutex sync.Mutex

// outcomeClient is the global GraphQL outcome poster
var outcomeClient *OutcomeClient

// reportInterval defines how often the report fires and outcomes are posted
const reportInterval = 60 * time.Second

// =============================================================================
// Operational limits loader
// =============================================================================

func loadOperationalLimits(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open limits file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read CSV: %w", err)
	}

	operationalLimits = make(map[string]OperationalLimit)

	for i := 1; i < len(records); i++ {
		if len(records[i]) < 4 {
			continue
		}

		system := records[i][0]
		sensorName := records[i][1]

		high, err := strconv.ParseFloat(records[i][2], 64)
		if err != nil {
			logError(fmt.Sprintf("Invalid high value for sensor=%s value=%s", sensorName, records[i][2]))
			continue
		}

		low, err := strconv.ParseFloat(records[i][3], 64)
		if err != nil {
			logError(fmt.Sprintf("Invalid low value for sensor=%s value=%s", sensorName, records[i][3]))
			continue
		}

		operationalLimits[sensorName] = OperationalLimit{
			SensorName:      sensorName,
			OperationalHigh: high,
			OperationalLow:  low,
		}

		if i <= 3 {
			logInfo(fmt.Sprintf("Loaded limit system=%s sensor=%s low=%.2f high=%.2f", system, sensorName, low, high))
		}
	}

	logInfo(fmt.Sprintf("Operational limits loaded count=%d", len(operationalLimits)))
	return nil
}

// =============================================================================
// Aggregation
// =============================================================================

func addReadingToAggregate(reading SensorReading) {
	aggregateMutex.Lock()
	defer aggregateMutex.Unlock()

	value, err := strconv.ParseFloat(reading.Value, 64)
	if err != nil {
		logError(fmt.Sprintf("Failed to parse value sensor=%s value=%s", reading.SensorKey(), reading.Value))
		return
	}

	sensorKey := reading.SensorKey()

	if _, hasLimit := operationalLimits[sensorKey]; !hasLimit {
		return
	}

	if sensorAggregates[sensorKey] == nil {
		sensorAggregates[sensorKey] = &SensorAggregate{}
	}
	sensorAggregates[sensorKey].Sum += value
	sensorAggregates[sensorKey].Count++

	// Preserve SensorID for outcome reporting
	if reading.SensorID != "" {
		sensorAggregates[sensorKey].SensorID = reading.SensorID
	}
}

// =============================================================================
// Status history — retained for future change-alerting use
// =============================================================================

func updateStatusHistory(machineName, newStatus string) {
	statusHistoryMutex.Lock()
	defer statusHistoryMutex.Unlock()
	machineStatusHistory[machineName] = &MachineStatusRecord{
		Status:    newStatus,
		UpdatedAt: time.Now(),
	}
}

// =============================================================================
// Reporting
// =============================================================================

func printAverageReport() {
	aggregateMutex.Lock()
	defer aggregateMutex.Unlock()

	if len(sensorAggregates) == 0 {
		logInfo("No readings to report for this window")
		return
	}

	windowEnd := time.Now()
	windowStart := windowEnd.Add(-reportInterval)

	logInfo("Generating 60-second average sensor report")

	fmt.Printf("\n=== AVERAGE SENSOR REPORT (60 second window) ===\n")
	fmt.Printf("Report Time: %s\n", windowEnd.Format("2006-01-02 15:04:05"))
	fmt.Println("  " + strings.Repeat("=", 130))

	belowCount := 0
	inRangeCount := 0
	aboveCount := 0
	goodCount := 0
	offlineCount := 0
	warningCount := 0

	machineStats := make(map[string]*MachineStatus)

	// Track sensor data per machine for outcome reporting
	type machineSensorData struct {
		faultSensors []FaultSensor
		allSensors   []string
		avgValues    map[string]float64
	}
	machineData := make(map[string]*machineSensorData)

	for sensorName, aggregate := range sensorAggregates {
		if aggregate.Count == 0 {
			continue
		}

		avgValue := aggregate.Sum / float64(aggregate.Count)
		limit := operationalLimits[sensorName]

		parts := strings.SplitN(sensorName, ":", 2)
		machineName := "UNKNOWN"
		if len(parts) == 2 {
			machineName = parts[0]
		}

		if machineStats[machineName] == nil {
			machineStats[machineName] = &MachineStatus{}
		}
		if machineData[machineName] == nil {
			machineData[machineName] = &machineSensorData{
				avgValues: make(map[string]float64),
			}
		}

		machineStats[machineName].TotalSensors++
		machineData[machineName].allSensors = append(machineData[machineName].allSensors, sensorName)
		machineData[machineName].avgValues[sensorName] = avgValue

		rangeSpan := limit.OperationalHigh - limit.OperationalLow
		var percentage float64
		var percentageStr string

		if rangeSpan > 0 {
			percentage = ((avgValue - limit.OperationalLow) / rangeSpan) * 100
			percentageStr = fmt.Sprintf("%6.2f%%", percentage)
		} else {
			percentageStr = "  N/A  "
			percentage = 50
		}

		if percentage >= 0 && percentage <= 100 {
			machineStats[machineName].AvgPercentage += percentage
		}

		var status string
		if avgValue > limit.OperationalHigh {
			status = "🔴 ABOVE RANGE"
			aboveCount++
			machineStats[machineName].AboveSensors++
			machineData[machineName].faultSensors = append(machineData[machineName].faultSensors,
				FaultSensor{SensorKey: sensorName, SensorID: aggregate.SensorID, AvgValue: avgValue})
		} else if avgValue < limit.OperationalLow {
			status = "🔴 BELOW RANGE"
			belowCount++
			machineStats[machineName].BelowSensors++
			machineData[machineName].faultSensors = append(machineData[machineName].faultSensors,
				FaultSensor{SensorKey: sensorName, SensorID: aggregate.SensorID, AvgValue: avgValue})
		} else {
			inRangeCount++
			if avgValue == 0 {
				status = "⚪ OFFLINE"
				offlineCount++
				machineStats[machineName].OfflineSensors++
			} else if percentage >= 20 && percentage <= 80 {
				status = "🟢 GOOD"
				goodCount++
				machineStats[machineName].GoodSensors++
			} else if percentage < 20 {
				status = "⚪ STANDBY"
				offlineCount++
				machineStats[machineName].OfflineSensors++
			} else {
				status = "🟡 WARNING"
				warningCount++
				machineStats[machineName].WarningSensors++
			}
		}

		fmt.Printf("  %-45s Avg: %8.2f | Range: [%8.2f - %8.2f] | %s | %-20s | Samples: %d\n",
			sensorName, avgValue, limit.OperationalLow, limit.OperationalHigh, percentageStr, status, aggregate.Count)
	}

	fmt.Println("  " + strings.Repeat("=", 130))
	fmt.Printf("  Sensor Summary: %d good (20-80%%) | %d warning (>80%%) | %d possibly offline (<20%%) | %d above range | %d below range\n",
		goodCount, warningCount, offlineCount, aboveCount, belowCount)

	_ = inRangeCount

	fmt.Println("\n=== MACHINE STATUS ===")
	fmt.Println("  " + strings.Repeat("=", 130))

	type MachineStatusJSON struct {
		Status         string  `json:"status"`
		Running        string  `json:"running"`
		AvgPercentage  float64 `json:"avg_percentage"`
		GoodSensors    int     `json:"good_sensors"`
		WarningSensors int     `json:"warning_sensors"`
		OfflineSensors int     `json:"offline_sensors"`
		FaultSensors   int     `json:"fault_sensors"`
		TotalSensors   int     `json:"total_sensors"`
		Timestamp      string  `json:"timestamp"`
	}

	machineStatusJSON := make(map[string]MachineStatusJSON)

	for machineName, stats := range machineStats {
		inRangeSensors := stats.GoodSensors + stats.WarningSensors + stats.OfflineSensors
		avgPercentage := 0.0
		if inRangeSensors > 0 {
			avgPercentage = stats.AvgPercentage / float64(inRangeSensors)
		}

		var machineStatus string
		var isRunning string

		offlineRatio := float64(stats.OfflineSensors) / float64(stats.TotalSensors)

		if stats.AboveSensors > 0 || stats.BelowSensors > 0 {
			machineStatus = "CRITICAL"
			isRunning = "RUNNING (FAULT)"
		} else if offlineRatio > 0.5 {
			machineStatus = "OFFLINE"
			isRunning = "STANDBY"
		} else if stats.WarningSensors > stats.GoodSensors {
			machineStatus = "WARNING"
			isRunning = "RUNNING"
		} else if stats.GoodSensors > 0 {
			machineStatus = "GOOD"
			isRunning = "RUNNING"
		} else {
			machineStatus = "UNCERTAIN"
			isRunning = "UNKNOWN"
		}

		fmt.Printf("  %-30s Status: %-20s | Running: %-20s | Avg: %6.2f%% | Sensors: %d good, %d warn, %d offline, %d fault\n",
			machineName, machineStatus, isRunning, avgPercentage,
			stats.GoodSensors, stats.WarningSensors, stats.OfflineSensors,
			stats.AboveSensors+stats.BelowSensors)

		logInfo(fmt.Sprintf("Machine status machine=%s status=%s running=%s avg_pct=%.2f good=%d warn=%d offline=%d fault=%d",
			machineName, machineStatus, isRunning, avgPercentage,
			stats.GoodSensors, stats.WarningSensors, stats.OfflineSensors,
			stats.AboveSensors+stats.BelowSensors))

		machineStatusJSON[machineName] = MachineStatusJSON{
			Status:         machineStatus,
			Running:        isRunning,
			AvgPercentage:  avgPercentage,
			GoodSensors:    stats.GoodSensors,
			WarningSensors: stats.WarningSensors,
			OfflineSensors: stats.OfflineSensors,
			FaultSensors:   stats.AboveSensors + stats.BelowSensors,
			TotalSensors:   stats.TotalSensors,
			Timestamp:      time.Now().Format(time.RFC3339),
		}

		// Update status history for future change-alerting use
		updateStatusHistory(machineName, machineStatus)

		// Post outcome every window unconditionally — 60-second heartbeat
		mData := machineData[machineName]
		payload := OutcomePayload{
			MachineName:    machineName,
			Status:         machineStatus,
			Running:        isRunning,
			FaultSensors:   mData.faultSensors,
			AllSensorNames: mData.allSensors,
			AvgPercentage:  avgPercentage,
			WindowStart:    windowStart,
			WindowEnd:      windowEnd,
		}
		go func(p OutcomePayload) {
			if err := outcomeClient.PostOutcome(p); err != nil {
				logError(fmt.Sprintf("Failed to post outcome machine=%s err=%s", p.MachineName, err))
			}
		}(payload)
	}

	fmt.Println("  " + strings.Repeat("=", 130))
	fmt.Println()

	jsonData, err := json.MarshalIndent(machineStatusJSON, "", "  ")
	if err == nil {
		if err = os.WriteFile("machine_status.json", jsonData, 0644); err != nil {
			logError(fmt.Sprintf("Could not write machine_status.json: %s", err))
		}
	}

	sensorAggregates = make(map[string]*SensorAggregate)
}

// =============================================================================
// Main
// =============================================================================

func main() {
	limitsFile := "files/sensor_operational_range.csv"
	if len(os.Args) > 1 {
		limitsFile = os.Args[1]
	}

	if err := loadOperationalLimits(limitsFile); err != nil {
		logError(fmt.Sprintf("Could not load operational limits file=%s err=%s", limitsFile, err))
		logInfo("Continuing without limit checking")
	}

	sensorAggregates = make(map[string]*SensorAggregate)
	machineStatusHistory = make(map[string]*MachineStatusRecord)
	lastReportTime = time.Now()

	// Initialize outcome client
	outcomeClient = NewOutcomeClient()

	// Connect to RabbitMQ — URL from env with localhost fallback for local dev
	mqURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	logInfo(fmt.Sprintf("Connecting to RabbitMQ url=%s", mqURL))
	conn, err := amqp.Dial(mqURL)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to connect to RabbitMQ: %s", err))
	}
	defer conn.Close()
	logInfo("Connected to RabbitMQ")

	ch, err := conn.Channel()
	if err != nil {
		logFatal(fmt.Sprintf("Failed to open channel: %s", err))
	}
	defer ch.Close()

	// Declare exchange
	exchangeName := "erm.ex.values"
	err = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to declare exchange=%s err=%s", exchangeName, err))
	}
	logInfo(fmt.Sprintf("Exchange declared name=%s type=topic", exchangeName))

	// Declare queue per IDD: auto_delete=TRUE, exclusive=FALSE
	queueName := "sensor-consumer-readings"
	q, err := ch.QueueDeclare(queueName, false, true, false, false, nil)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to declare queue=%s err=%s", queueName, err))
	}

	// Bind queue to exchange
	routingKey := "#"
	err = ch.QueueBind(q.Name, routingKey, exchangeName, false, nil)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to bind queue=%s exchange=%s err=%s", queueName, exchangeName, err))
	}
	logInfo(fmt.Sprintf("Queue bound queue=%s exchange=%s routing_key=%s", queueName, exchangeName, routingKey))

	err = ch.Qos(1, 0, false)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to set QoS: %s", err))
	}

	consumerTag := "sensor-consumer-01"
	msgs, err := ch.Consume(q.Name, consumerTag, false, false, false, false, nil)
	if err != nil {
		logFatal(fmt.Sprintf("Failed to register consumer: %s", err))
	}
	logInfo(fmt.Sprintf("Consumer registered queue=%s consumer_tag=%s", queueName, consumerTag))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	done := make(chan bool)

	ticker := time.NewTicker(reportInterval)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			printAverageReport()
		}
	}()

	go func() {
		messageCount := 0
		for d := range msgs {
			var reading SensorReading
			if err := json.Unmarshal(d.Body, &reading); err != nil {
				logError(fmt.Sprintf("Failed to parse message delivery_tag=%d err=%s", d.DeliveryTag, err))
				d.Ack(false)
				continue
			}
			addReadingToAggregate(reading)
			messageCount++
			d.Ack(false)

			if messageCount%100 == 0 {
				logInfo(fmt.Sprintf("Messages processed count=%d", messageCount))
			}
		}
		done <- true
	}()

	logInfo(fmt.Sprintf("Consumer started exchange=%s routing_key=%s report_interval=%s",
		exchangeName, routingKey, reportInterval))
	logInfo("Press CTRL+C to exit")

	<-sigChan
	logInfo("Shutdown signal received — shutting down gracefully")

	printAverageReport()
	ch.Close()
	<-done

	logInfo("Consumer stopped")
}
