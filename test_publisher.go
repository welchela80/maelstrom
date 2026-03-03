// test_publisher.go
// Publishes all CSV files in a directory to erm.ex.values.
//
// Usage:
//
//	go run test_publisher.go <path-to-csv-or-directory>
//	go run test_publisher.go ../files/AC_1_data.csv
//	go run test_publisher.go /path/to/csv/directory
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName = "erm.ex.values"
	mqURL        = "amqp://guest:guest@localhost:5672/"
	systemName   = "AC Plant"
)

type PDTSensorValue struct {
	SystemName  string `json:"SystemName"`
	MachineName string `json:"MachineName"`
	SensorName  string `json:"SensorName"`
	Value       string `json:"Value"`
	SensorID    string `json:"SensorID"`
}

func (p PDTSensorValue) RoutingKey() string {
	return fmt.Sprintf("%s.%s.%s", p.SystemName, p.MachineName, p.SensorName)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run test_publisher.go <path-to-csv-or-directory>")
		os.Exit(1)
	}
	target := os.Args[1]

	// Connect to RabbitMQ
	conn, err := amqp.Dial(mqURL)
	if err != nil {
		fmt.Printf("Failed to connect to RabbitMQ: %s\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("Connected to RabbitMQ")

	ch, err := conn.Channel()
	if err != nil {
		fmt.Printf("Failed to open channel: %s\n", err)
		os.Exit(1)
	}
	defer ch.Close()

	err = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)
	if err != nil {
		fmt.Printf("Failed to declare exchange: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Exchange ready: %s\n", exchangeName)

	// Collect CSV files — single file or recursive directory walk
	var csvFiles []string
	info, err := os.Stat(target)
	if err != nil {
		fmt.Printf("Cannot access path: %s\n", err)
		os.Exit(1)
	}

	if info.IsDir() {
		err = filepath.Walk(target, func(path string, f os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".csv") {
				csvFiles = append(csvFiles, path)
			}
			return nil
		})
		if err != nil {
			fmt.Printf("Failed to walk directory: %s\n", err)
			os.Exit(1)
		}
	} else {
		csvFiles = []string{target}
	}

	if len(csvFiles) == 0 {
		fmt.Println("No CSV files found")
		os.Exit(1)
	}

	fmt.Printf("Found %d CSV file(s) to publish\n\n", len(csvFiles))

	// Publish each file
	grandTotal := 0
	for fileIdx, csvPath := range csvFiles {
		fmt.Printf("[%d/%d] Publishing %s\n", fileIdx+1, len(csvFiles), filepath.Base(csvPath))

		count, err := publishCSV(ch, csvPath)
		if err != nil {
			fmt.Printf("  ERROR: %s\n", err)
			continue
		}

		grandTotal += count
		fmt.Printf("  Done — %d messages published\n\n", count)
	}

	fmt.Printf("All files complete. Total messages published: %d\n", grandTotal)
}

func publishCSV(ch *amqp.Channel, csvPath string) (int, error) {
	file, err := os.Open(csvPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return 0, fmt.Errorf("failed to read headers: %w", err)
	}

	// Parse "MachineName:SensorName" headers
	type SensorMeta struct {
		MachineName string
		SensorName  string
	}
	sensors := make([]SensorMeta, len(headers))
	for i, h := range headers {
		parts := strings.SplitN(strings.TrimSpace(h), ":", 2)
		if len(parts) == 2 {
			sensors[i] = SensorMeta{MachineName: parts[0], SensorName: parts[1]}
		} else {
			sensors[i] = SensorMeta{MachineName: "UNKNOWN", SensorName: h}
		}
	}

	rows, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("failed to read rows: %w", err)
	}

	totalPublished := 0
	for rowIdx, row := range rows {
		for colIdx, value := range row {
			if colIdx >= len(sensors) {
				continue
			}

			reading := PDTSensorValue{
				SystemName:  systemName,
				MachineName: sensors[colIdx].MachineName,
				SensorName:  sensors[colIdx].SensorName,
				Value:       strings.TrimSpace(value),
				SensorID:    "",
			}

			body, err := json.Marshal(reading)
			if err != nil {
				continue
			}

			err = ch.PublishWithContext(
				context.Background(),
				exchangeName,
				reading.RoutingKey(),
				false,
				false,
				amqp.Publishing{
					ContentType:  "application/json",
					DeliveryMode: amqp.Transient,
					Timestamp:    time.Now(),
					Body:         body,
				},
			)
			if err != nil {
				continue
			}
			totalPublished++
		}

		// 100ms delay between rows to simulate sensor timing
		time.Sleep(100 * time.Millisecond)

		if (rowIdx+1)%10 == 0 {
			fmt.Printf("  Row %d/%d\n", rowIdx+1, len(rows))
		}
	}

	return totalPublished, nil
}
