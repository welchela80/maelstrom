// consumer.go
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type SensorReading struct {
	Timestamp string            `json:"timestamp"`
	Readings  map[string]string `json:"readings"`
}

type OperationalLimit struct {
	SensorName      string
	OperationalHigh float64
	OperationalLow  float64
}

type SensorAggregate struct {
	Sum   float64
	Count int
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

var operationalLimits map[string]OperationalLimit
var sensorAggregates map[string]*SensorAggregate
var aggregateMutex sync.Mutex
var lastReportTime time.Time

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

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

	// Skip header row
	for i := 1; i < len(records); i++ {
		if len(records[i]) < 3 {
			continue
		}

		sensorName := records[i][0]
		high, err := strconv.ParseFloat(records[i][1], 64)
		if err != nil {
			log.Printf("Warning: Invalid high value for %s: %s", sensorName, records[i][1])
			continue
		}

		low, err := strconv.ParseFloat(records[i][2], 64)
		if err != nil {
			log.Printf("Warning: Invalid low value for %s: %s", sensorName, records[i][2])
			continue
		}

		operationalLimits[sensorName] = OperationalLimit{
			SensorName:      sensorName,
			OperationalHigh: high,
			OperationalLow:  low,
		}
	}

	log.Printf("Loaded %d operational limits", len(operationalLimits))
	return nil
}

func checkSensorValue(sensorName string, valueStr string) (bool, string) {
	// Try to parse the value
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		// Value might be a state/string, skip numeric checks
		return false, ""
	}

	// Check if we have limits for this sensor
	limit, exists := operationalLimits[sensorName]
	if !exists {
		return false, ""
	}

	// Check if value is out of range
	if value > limit.OperationalHigh {
		msg := fmt.Sprintf("⚠️  ALERT: %s = %.2f (ABOVE HIGH LIMIT: %.2f)",
			sensorName, value, limit.OperationalHigh)
		return true, msg
	} else if value < limit.OperationalLow {
		msg := fmt.Sprintf("⚠️  ALERT: %s = %.2f (BELOW LOW LIMIT: %.2f)",
			sensorName, value, limit.OperationalLow)
		return true, msg
	}

	return false, ""
}

func addReadingToAggregate(reading SensorReading) {
	aggregateMutex.Lock()
	defer aggregateMutex.Unlock()

	for sensorName, valueStr := range reading.Readings {
		// Try to parse the value
		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			continue
		}

		// Check if we have a limit for this sensor
		_, hasLimit := operationalLimits[sensorName]
		if !hasLimit {
			continue
		}

		// Add to aggregate
		if sensorAggregates[sensorName] == nil {
			sensorAggregates[sensorName] = &SensorAggregate{Sum: 0, Count: 0}
		}
		sensorAggregates[sensorName].Sum += value
		sensorAggregates[sensorName].Count++
	}
}

func printAverageReport() {
	aggregateMutex.Lock()
	defer aggregateMutex.Unlock()

	if len(sensorAggregates) == 0 {
		log.Println("No readings to report")
		return
	}

	fmt.Printf("\n=== AVERAGE SENSOR REPORT (10 second window) ===\n")
	fmt.Printf("Report Time: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println("  " + strings.Repeat("=", 130))

	// Track statistics
	belowCount := 0
	inRangeCount := 0
	aboveCount := 0
	goodCount := 0
	offlineCount := 0
	warningCount := 0

	// Track machine-level status
	machineStats := make(map[string]*MachineStatus)

	// Calculate and display averages
	for sensorName, aggregate := range sensorAggregates {
		if aggregate.Count == 0 {
			continue
		}

		avgValue := aggregate.Sum / float64(aggregate.Count)
		limit := operationalLimits[sensorName]

		// Extract machine name from sensor name (format: "MACHINE:SENSOR")
		parts := strings.SplitN(sensorName, ":", 2)
		machineName := "UNKNOWN"
		if len(parts) == 2 {
			machineName = parts[0]
		}

		// Initialize machine stats if needed
		if machineStats[machineName] == nil {
			machineStats[machineName] = &MachineStatus{}
		}
		machineStats[machineName].TotalSensors++

		// Calculate percentage of range
		rangeSpan := limit.OperationalHigh - limit.OperationalLow
		var percentage float64
		var percentageStr string

		if rangeSpan > 0 {
			percentage = ((avgValue - limit.OperationalLow) / rangeSpan) * 100
			percentageStr = fmt.Sprintf("%6.2f%%", percentage)
		} else {
			percentageStr = "  N/A  "
			percentage = 50 // Default to middle if no range
		}

		// Add to machine average percentage
		if percentage >= 0 && percentage <= 100 {
			machineStats[machineName].AvgPercentage += percentage
		}

		// Determine status with refined labels
		var status string
		if avgValue > limit.OperationalHigh {
			status = "🔴 ABOVE RANGE"
			aboveCount++
			machineStats[machineName].AboveSensors++
		} else if avgValue < limit.OperationalLow {
			status = "🔴 BELOW RANGE"
			belowCount++
			machineStats[machineName].BelowSensors++
		} else {
			inRangeCount++
			// Within range - check percentage for fine-tuned status
			if avgValue == 0 {
				status = "⚫ OFFLINE"
				offlineCount++
				machineStats[machineName].OfflineSensors++
			} else if percentage >= 20 && percentage <= 80 {
				status = "🟢 GOOD"
				goodCount++
				machineStats[machineName].GoodSensors++
			} else if percentage < 20 {
				status = "🔵 POSSIBLY OFFLINE"
				offlineCount++
				machineStats[machineName].OfflineSensors++
			} else { // percentage > 80
				status = "🟡 WARNING"
				warningCount++
				machineStats[machineName].WarningSensors++
			}
		}

		fmt.Printf("  %-45s Avg: %8.2f | Range: [%8.2f - %8.2f] | %s | %-20s | Samples: %d\n",
			sensorName, avgValue, limit.OperationalLow, limit.OperationalHigh, percentageStr, status, aggregate.Count)
	}

	// Display sensor summary
	fmt.Println("  " + strings.Repeat("=", 130))
	fmt.Printf("  Sensor Summary: %d good (20-80%%) | %d warning (>80%%) | %d possibly offline (<20%%) | %d above range | %d below range\n",
		goodCount, warningCount, offlineCount, aboveCount, belowCount)

	// Display machine-level status
	fmt.Println("\n=== MACHINE STATUS ===")
	fmt.Println("  " + strings.Repeat("=", 130))

	for machineName, stats := range machineStats {
		// Calculate average percentage for sensors in range
		inRangeSensors := stats.GoodSensors + stats.WarningSensors + stats.OfflineSensors
		avgPercentage := 0.0
		if inRangeSensors > 0 {
			avgPercentage = stats.AvgPercentage / float64(inRangeSensors)
		}

		// Determine machine status
		var machineStatus string
		var isRunning string

		// Machine is offline if >50% of sensors are offline/possibly offline
		offlineRatio := float64(stats.OfflineSensors) / float64(stats.TotalSensors)

		// Machine has critical issues if any sensors are above/below range
		if stats.AboveSensors > 0 || stats.BelowSensors > 0 {
			machineStatus = "🔴 CRITICAL"
			isRunning = "RUNNING (FAULT)"
		} else if offlineRatio > 0.5 {
			machineStatus = "⚪ OFFLINE"
			isRunning = "NOT RUNNING"
		} else if stats.WarningSensors > stats.GoodSensors {
			machineStatus = "🟡 WARNING"
			isRunning = "RUNNING"
		} else if stats.GoodSensors > 0 {
			machineStatus = "🟢 GOOD"
			isRunning = "RUNNING"
		} else {
			machineStatus = "🔵 UNCERTAIN"
			isRunning = "UNKNOWN"
		}

		fmt.Printf("  %-30s Status: %-20s | Running: %-20s | Avg: %6.2f%% | Sensors: %d good, %d warn, %d offline, %d fault\n",
			machineName, machineStatus, isRunning, avgPercentage,
			stats.GoodSensors, stats.WarningSensors, stats.OfflineSensors,
			stats.AboveSensors+stats.BelowSensors)
	}

	fmt.Println("  " + strings.Repeat("=", 130))
	fmt.Println()

	// Reset aggregates for next window
	sensorAggregates = make(map[string]*SensorAggregate)
}

func main() {
	// Load operational limits
	limitsFile := "files/sensor_operational_range.csv"
	if len(os.Args) > 1 {
		limitsFile = os.Args[1]
	}

	err := loadOperationalLimits(limitsFile)
	if err != nil {
		log.Printf("Warning: Could not load operational limits: %s", err)
		log.Println("Continuing without limit checking...")
	}

	// Initialize aggregates
	sensorAggregates = make(map[string]*SensorAggregate)
	lastReportTime = time.Now()

	// Connect to RabbitMQ
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()

	queueName := "sensor_readings"
	q, err := ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	failOnError(err, "Failed to declare a queue")

	// Set QoS
	err = ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	failOnError(err, "Failed to set QoS")

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	// Channel for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Channel to signal goroutine completion
	done := make(chan bool)

	// Start 10-second ticker for reports
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Start report ticker goroutine
	go func() {
		for range ticker.C {
			printAverageReport()
		}
	}()

	// Start consumer goroutine
	go func() {
		messageCount := 0
		for d := range msgs {
			var reading SensorReading
			err := json.Unmarshal(d.Body, &reading)
			if err != nil {
				log.Printf("Error parsing message: %s", err)
				d.Nack(false, false)
				continue
			}

			// Add reading to aggregate
			addReadingToAggregate(reading)
			messageCount++

			// Acknowledge message
			d.Ack(false)
		}
		done <- true
	}()

	log.Printf("Consumer started. Waiting for messages on queue '%s'...", queueName)
	log.Printf("Reports will be generated every 10 seconds")
	log.Printf("Press CTRL+C to exit")

	// Wait for interrupt signal
	<-sigChan
	log.Println("\nShutting down gracefully...")

	// Print final report
	printAverageReport()

	// Close channel to stop consuming
	ch.Close()

	// Wait for goroutine to finish
	<-done

	log.Println("Consumer stopped")
}
