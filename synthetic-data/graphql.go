// graphql.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// =============================================================================
// ERM GraphQL Outcome Client (IDD v1.1.4 §GraphQL Protocol)
// Stubbed for local development — set GRAPHQL_STUB=true to log instead of POST
// =============================================================================

type OutcomeClient struct {
	GraphQLURL  string
	JWTUsername string
	JWTPassword string
	ProviderID  string
	FaultIDs    map[string]string // keyed by machine status
	stub        bool
}

type OutcomePayload struct {
	MachineName    string
	Status         string
	Running        string
	FaultSensors   []FaultSensor
	AllSensorNames []string
	AvgPercentage  float64
	WindowStart    time.Time
	WindowEnd      time.Time
}

type FaultSensor struct {
	SensorKey string
	SensorID  string
	AvgValue  float64
}

// loginMutation fetches a JWT from the ERM GraphQL API.
// Stubbed when GRAPHQL_STUB=true.
type loginResponse struct {
	Data struct {
		Login struct {
			Token string `json:"token"`
		} `json:"login"`
	} `json:"data"`
}

type gqlRequest struct {
	Query string `json:"query"`
}

// NewOutcomeClient initializes the client from environment variables.
func NewOutcomeClient() *OutcomeClient {
	stub := os.Getenv("GRAPHQL_STUB") != "false"

	client := &OutcomeClient{
		GraphQLURL:  getEnv("GRAPHQL_URL", "http://localhost:9001/graphql"),
		JWTUsername: getEnv("JWT_USERNAME", "admin"),
		JWTPassword: getEnv("JWT_PASSWORD", "admin@1"),
		ProviderID:  getEnv("PROVIDER_ID", "00000000-0000-0000-0000-000000000000"),
		stub:        stub,
		FaultIDs: map[string]string{
			"CRITICAL":  getEnv("FAULT_ID_CRITICAL", "00000000-0000-0000-0000-000000000001"),
			"WARNING":   getEnv("FAULT_ID_WARNING", "00000000-0000-0000-0000-000000000002"),
			"GOOD":      getEnv("FAULT_ID_GOOD", "00000000-0000-0000-0000-000000000003"),
			"OFFLINE":   getEnv("FAULT_ID_OFFLINE", "00000000-0000-0000-0000-000000000004"),
			"UNCERTAIN": getEnv("FAULT_ID_UNCERTAIN", "00000000-0000-0000-0000-000000000005"),
		},
	}

	if stub {
		logInfo("OutcomeClient initialized in STUB mode — outcomes will be logged, not posted")
	} else {
		logInfo(fmt.Sprintf("OutcomeClient initialized url=%s provider=%s", client.GraphQLURL, client.ProviderID))
	}

	return client
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// fetchJWT authenticates and returns a bearer token.
func (c *OutcomeClient) fetchJWT() (string, error) {
	if c.stub {
		return "stub-token", nil
	}

	query := fmt.Sprintf(`mutation { login(username: "%s", password: "%s") { token } }`,
		c.JWTUsername, c.JWTPassword)

	body, err := json.Marshal(gqlRequest{Query: query})
	if err != nil {
		return "", err
	}

	resp, err := http.Post(c.GraphQLURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	var loginResp loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", fmt.Errorf("failed to decode login response: %w", err)
	}

	if loginResp.Data.Login.Token == "" {
		return "", fmt.Errorf("empty token returned from login")
	}

	return loginResp.Data.Login.Token, nil
}

// PostOutcome fires a createAnalyticEvent mutation for the given machine status.
// In stub mode it logs the full payload instead of posting.
func (c *OutcomeClient) PostOutcome(payload OutcomePayload) error {
	faultID, ok := c.FaultIDs[payload.Status]
	if !ok {
		faultID = c.FaultIDs["UNCERTAIN"]
	}

	// Build sensor ID list from fault sensors
	sensorIDs := make([]string, 0, len(payload.FaultSensors))
	for _, s := range payload.FaultSensors {
		if s.SensorID != "" {
			sensorIDs = append(sensorIDs, fmt.Sprintf(`"%s"`, s.SensorID))
		}
	}

	// Build metadata entries
	metaEntries := []string{
		fmt.Sprintf(`{metakey:"machine", metavalue:"%s"}`, payload.MachineName),
		fmt.Sprintf(`{metakey:"status", metavalue:"%s"}`, payload.Status),
		fmt.Sprintf(`{metakey:"running", metavalue:"%s"}`, payload.Running),
		fmt.Sprintf(`{metakey:"avg_percentage", metavalue:"%.2f"}`, payload.AvgPercentage),
	}

	// Add each fault sensor as metadata
	for _, s := range payload.FaultSensors {
		metaEntries = append(metaEntries,
			fmt.Sprintf(`{metakey:"fault_sensor", metavalue:"%s avg=%.2f"}`, s.SensorKey, s.AvgValue))
	}

	// Build telemetry JSON
	type telemetryData struct {
		MachineName   string        `json:"machine_name"`
		Status        string        `json:"status"`
		AvgPercentage float64       `json:"avg_percentage"`
		FaultSensors  []FaultSensor `json:"fault_sensors"`
		WindowStart   string        `json:"window_start"`
		WindowEnd     string        `json:"window_end"`
	}
	telemetry := telemetryData{
		MachineName:   payload.MachineName,
		Status:        payload.Status,
		AvgPercentage: payload.AvgPercentage,
		FaultSensors:  payload.FaultSensors,
		WindowStart:   payload.WindowStart.Format(time.RFC3339),
		WindowEnd:     payload.WindowEnd.Format(time.RFC3339),
	}
	telemetryJSON, _ := json.Marshal(telemetry)
	// Escape for embedding in GraphQL string
	telemetryStr := strings.ReplaceAll(string(telemetryJSON), `"`, `\"`)

	reason := fmt.Sprintf("Machine %s status: %s (avg %.2f%%)",
		payload.MachineName, payload.Status, payload.AvgPercentage)

	mutation := fmt.Sprintf(`mutation {
		createAnalyticEvent(input: {
			metadata: {
				internalID: "%s-%s"
				metadata: [%s]
			},
			body: {
				faultID: "%s",
				reason: "%s",
				name: "%s status assessment",
				providerID: "%s",
				sensorIDs: [%s],
				telemetry: "%s",
				start: "%s",
				end: "%s"
			}
		}) { id }
	}`,
		payload.MachineName, payload.Status,
		strings.Join(metaEntries, ", "),
		faultID,
		reason,
		payload.MachineName,
		c.ProviderID,
		strings.Join(sensorIDs, ", "),
		telemetryStr,
		payload.WindowStart.Format(time.RFC3339),
		payload.WindowEnd.Format(time.RFC3339),
	)

	if c.stub {
		// Log the full payload instead of posting
		type stubLog struct {
			Machine     string        `json:"machine"`
			Status      string        `json:"status"`
			FaultID     string        `json:"fault_id"`
			ProviderID  string        `json:"provider_id"`
			Reason      string        `json:"reason"`
			Sensors     []FaultSensor `json:"fault_sensors"`
			WindowStart string        `json:"window_start"`
			WindowEnd   string        `json:"window_end"`
		}
		sl := stubLog{
			Machine:     payload.MachineName,
			Status:      payload.Status,
			FaultID:     faultID,
			ProviderID:  c.ProviderID,
			Reason:      reason,
			Sensors:     payload.FaultSensors,
			WindowStart: payload.WindowStart.Format(time.RFC3339),
			WindowEnd:   payload.WindowEnd.Format(time.RFC3339),
		}
		slJSON, _ := json.Marshal(sl)
		logInfo(fmt.Sprintf("STUB outcome machine=%s status=%s payload=%s",
			payload.MachineName, payload.Status, string(slJSON)))
		return nil
	}

	// Live path — fetch JWT and POST mutation
	token, err := c.fetchJWT()
	if err != nil {
		return fmt.Errorf("failed to get JWT: %w", err)
	}

	body, err := json.Marshal(gqlRequest{Query: mutation})
	if err != nil {
		return fmt.Errorf("failed to marshal mutation: %w", err)
	}

	req, err := http.NewRequest("POST", c.GraphQLURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post outcome: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	logInfo(fmt.Sprintf("Outcome posted machine=%s status=%s response=%v",
		payload.MachineName, payload.Status, result))

	return nil
}
