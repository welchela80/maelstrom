// analytics.go
package main

import (
	"math"
	"sync"
	"time"
)

// TrendAnalysis contains regression and trend information
type TrendAnalysis struct {
	Slope           float64 // Rate of change per second
	Intercept       float64
	RSquared        float64 // How well the trend fits (0-1)
	Prediction5Min  float64 // Predicted value in 5 minutes
	Prediction10Min float64 // Predicted value in 10 minutes
	TrendDirection  string  // "INCREASING", "DECREASING", "STABLE"
	HealthScore     float64 // 0-100, based on trend and position in range
	TimeToWarning   int     // Seconds until sensor enters warning zone (80%)
	TimeToCritical  int     // Seconds until sensor exceeds limits
	Confidence      string  // "HIGH", "MEDIUM", "LOW" based on R-squared
}

// SensorTrendData holds historical data for trend analysis
type SensorTrendData struct {
	Values     []float64
	Timestamps []time.Time
	MaxPoints  int
	mutex      sync.RWMutex
}

// MachineTrend aggregates trends for all sensors on a machine
type MachineTrend struct {
	MachineName       string
	OverallTrend      string  // "IMPROVING", "DEGRADING", "STABLE"
	HealthScore       float64 // Aggregate health score
	SensorsAtRisk     int     // Number of sensors trending toward critical
	EstimatedFailTime int     // Seconds until estimated failure (if degrading)
	Confidence        string
}

var sensorTrends map[string]*SensorTrendData
var trendMutex sync.RWMutex

func initAnalytics() {
	sensorTrends = make(map[string]*SensorTrendData)
}

// AddDataPoint adds a new sensor reading to the trend analysis
func AddDataPoint(sensorName string, value float64, timestamp time.Time) {
	trendMutex.Lock()
	defer trendMutex.Unlock()

	if sensorTrends[sensorName] == nil {
		sensorTrends[sensorName] = &SensorTrendData{
			Values:     make([]float64, 0, 100),
			Timestamps: make([]time.Time, 0, 100),
			MaxPoints:  100, // Keep last 100 points
		}
	}

	trend := sensorTrends[sensorName]
	trend.mutex.Lock()
	defer trend.mutex.Unlock()

	trend.Values = append(trend.Values, value)
	trend.Timestamps = append(trend.Timestamps, timestamp)

	// Keep only last MaxPoints
	if len(trend.Values) > trend.MaxPoints {
		trend.Values = trend.Values[1:]
		trend.Timestamps = trend.Timestamps[1:]
	}
}

// CalculateLinearRegression performs least squares regression
func CalculateLinearRegression(x, y []float64) (slope, intercept, rSquared float64) {
	n := float64(len(x))
	if n < 2 {
		return 0, 0, 0
	}

	var sumX, sumY, sumXY, sumXX, sumYY float64

	for i := 0; i < len(x); i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumXX += x[i] * x[i]
		sumYY += y[i] * y[i]
	}

	// Calculate slope and intercept
	slope = (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
	intercept = (sumY - slope*sumX) / n

	// Calculate R-squared
	meanY := sumY / n
	var ssTotal, ssResidual float64
	for i := 0; i < len(x); i++ {
		predicted := slope*x[i] + intercept
		ssTotal += (y[i] - meanY) * (y[i] - meanY)
		ssResidual += (y[i] - predicted) * (y[i] - predicted)
	}

	if ssTotal > 0 {
		rSquared = 1 - (ssResidual / ssTotal)
	}

	return slope, intercept, rSquared
}

// AnalyzeSensorTrend performs comprehensive trend analysis on a sensor
func AnalyzeSensorTrend(sensorName string, limit OperationalLimit) *TrendAnalysis {
	trendMutex.RLock()
	trendData, exists := sensorTrends[sensorName]
	trendMutex.RUnlock()

	if !exists || trendData == nil {
		return nil
	}

	trendData.mutex.RLock()
	defer trendData.mutex.RUnlock()

	if len(trendData.Values) < 3 {
		return nil // Need at least 3 points for meaningful analysis
	}

	// Convert timestamps to seconds since first point
	x := make([]float64, len(trendData.Timestamps))
	y := trendData.Values

	baseTime := trendData.Timestamps[0]
	for i, t := range trendData.Timestamps {
		x[i] = t.Sub(baseTime).Seconds()
	}

	// Perform regression
	slope, intercept, rSquared := CalculateLinearRegression(x, y)

	analysis := &TrendAnalysis{
		Slope:     slope,
		Intercept: intercept,
		RSquared:  rSquared,
	}

	// Predict future values
	currentTime := x[len(x)-1]
	analysis.Prediction5Min = slope*(currentTime+300) + intercept
	analysis.Prediction10Min = slope*(currentTime+600) + intercept

	// Determine trend direction
	if math.Abs(slope) < 0.001 {
		analysis.TrendDirection = "STABLE"
	} else if slope > 0 {
		analysis.TrendDirection = "INCREASING"
	} else {
		analysis.TrendDirection = "DECREASING"
	}

	// Determine confidence based on R-squared
	if rSquared > 0.8 {
		analysis.Confidence = "HIGH"
	} else if rSquared > 0.5 {
		analysis.Confidence = "MEDIUM"
	} else {
		analysis.Confidence = "LOW"
	}

	// Calculate current position in operational range
	rangeSpan := limit.OperationalHigh - limit.OperationalLow
	currentValue := y[len(y)-1]
	var currentPercentage float64

	if rangeSpan > 0 {
		currentPercentage = ((currentValue - limit.OperationalLow) / rangeSpan) * 100
	} else {
		currentPercentage = 50
	}

	// Calculate health score (0-100)
	// Best health: stable trend, value between 20-80%
	healthScore := 100.0

	// Penalize for being far from ideal range
	if currentPercentage < 20 {
		healthScore -= (20 - currentPercentage) * 2
	} else if currentPercentage > 80 {
		healthScore -= (currentPercentage - 80) * 2
	}

	// Penalize for negative trends
	if slope > 0 && currentPercentage > 50 {
		// Increasing when already high
		healthScore -= math.Abs(slope) * 100
	} else if slope < 0 && currentPercentage < 50 {
		// Decreasing when already low
		healthScore -= math.Abs(slope) * 100
	}

	// Bonus for stable trends
	if analysis.TrendDirection == "STABLE" {
		healthScore += 10
	}

	analysis.HealthScore = math.Max(0, math.Min(100, healthScore))

	// Calculate time to warning (80% threshold)
	if slope > 0 && currentPercentage < 80 {
		warningThreshold := limit.OperationalLow + (rangeSpan * 0.8)
		if currentValue < warningThreshold {
			timeToWarning := (warningThreshold - currentValue) / slope
			analysis.TimeToWarning = int(timeToWarning)
		}
	} else if slope < 0 && currentPercentage > 20 {
		warningThreshold := limit.OperationalLow + (rangeSpan * 0.2)
		if currentValue > warningThreshold {
			timeToWarning := (currentValue - warningThreshold) / math.Abs(slope)
			analysis.TimeToWarning = int(timeToWarning)
		}
	}

	// Calculate time to critical (exceeding limits)
	if slope > 0 && currentValue < limit.OperationalHigh {
		timeToCritical := (limit.OperationalHigh - currentValue) / slope
		analysis.TimeToCritical = int(timeToCritical)
	} else if slope < 0 && currentValue > limit.OperationalLow {
		timeToCritical := (currentValue - limit.OperationalLow) / math.Abs(slope)
		analysis.TimeToCritical = int(timeToCritical)
	}

	return analysis
}

// AnalyzeMachineTrends aggregates sensor trends for a machine
func AnalyzeMachineTrends(machineName string, machineStats *MachineStatus) *MachineTrend {
	// Get all sensors for this machine
	var sensorAnalyses []*TrendAnalysis
	var healthScores []float64
	sensorsAtRisk := 0

	trendMutex.RLock()
	for sensorName := range sensorTrends {
		// Check if sensor belongs to this machine
		if len(sensorName) > len(machineName) && sensorName[:len(machineName)] == machineName {
			if limit, exists := operationalLimits[sensorName]; exists {
				analysis := AnalyzeSensorTrend(sensorName, limit)
				if analysis != nil && analysis.Confidence != "LOW" {
					sensorAnalyses = append(sensorAnalyses, analysis)
					healthScores = append(healthScores, analysis.HealthScore)

					// Check if sensor is at risk
					if analysis.TimeToWarning > 0 && analysis.TimeToWarning < 600 {
						sensorsAtRisk++
					}
				}
			}
		}
	}
	trendMutex.RUnlock()

	if len(sensorAnalyses) == 0 {
		return nil
	}

	machineTrend := &MachineTrend{
		MachineName:   machineName,
		SensorsAtRisk: sensorsAtRisk,
	}

	// Calculate average health score
	var totalHealth float64
	for _, score := range healthScores {
		totalHealth += score
	}
	machineTrend.HealthScore = totalHealth / float64(len(healthScores))

	// Determine overall trend
	degradingCount := 0
	improvingCount := 0
	stableCount := 0

	for _, analysis := range sensorAnalyses {
		if analysis.HealthScore < 60 {
			degradingCount++
		} else if analysis.HealthScore > 80 {
			improvingCount++
		} else {
			stableCount++
		}
	}

	if degradingCount > len(sensorAnalyses)/3 {
		machineTrend.OverallTrend = "DEGRADING"
	} else if improvingCount > len(sensorAnalyses)/3 {
		machineTrend.OverallTrend = "IMPROVING"
	} else {
		machineTrend.OverallTrend = "STABLE"
	}

	// Estimate time to failure (most critical sensor)
	minTimeToCritical := math.MaxInt32
	for _, analysis := range sensorAnalyses {
		if analysis.TimeToCritical > 0 && analysis.TimeToCritical < minTimeToCritical {
			minTimeToCritical = analysis.TimeToCritical
		}
	}

	if minTimeToCritical < math.MaxInt32 {
		machineTrend.EstimatedFailTime = minTimeToCritical
	}

	// Overall confidence
	highConfCount := 0
	for _, analysis := range sensorAnalyses {
		if analysis.Confidence == "HIGH" {
			highConfCount++
		}
	}

	if highConfCount > len(sensorAnalyses)/2 {
		machineTrend.Confidence = "HIGH"
	} else if highConfCount > len(sensorAnalyses)/4 {
		machineTrend.Confidence = "MEDIUM"
	} else {
		machineTrend.Confidence = "LOW"
	}

	return machineTrend
}
