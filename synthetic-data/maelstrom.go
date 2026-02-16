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
	// Initialize base analytics
	sensorTrends = make(map[string]*SensorTrendData)

	// Initialize advanced analytics structures
	machineStates = make(map[string]*OperationalState)

	// Initialize system model
	systemModel = &SystemModel{
		SensorGroups:      make(map[string]SensorGroup),
		InverseRelations:  make([]InverseRelation, 0),
		CausalChains:      make(map[string]CausalChain),
		OperationalStates: make(map[string]OperationalState),
	}

	// Define sensor correlation groups
	defineSystemModel()
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

// Advanced analytics structures
type SystemModel struct {
	SensorGroups      map[string]SensorGroup
	InverseRelations  []InverseRelation
	CausalChains      map[string]CausalChain
	OperationalStates map[string]OperationalState
}

type SensorGroup struct {
	Name        string
	Sensors     []string
	Correlation float64
	Description string
}

type InverseRelation struct {
	Group1 []string
	Group2 []string
}

type CausalChain struct {
	Trigger    string
	Immediate  []string // 0-5 seconds
	ShortDelay []string // 5-30 seconds
	LongDelay  []string // 30-60 seconds
}

type OperationalState struct {
	MachineName    string
	State          string // "STARTING", "RUNNING", "STOPPING", "STOPPED", "FAULTED"
	StateStartTime time.Time
	ExpectedValues map[string]float64 // Expected sensor values for this state
}

// Enhanced trend analysis with system awareness
type SystemTrendAnalysis struct {
	SensorName        string
	Slope             float64
	RSquared          float64
	CorrelationScore  float64 // How well sensor correlates with related sensors
	CausalityScore    float64 // How well cause-effect chains are working
	StateConsistency  float64 // How consistent sensor is with machine state
	AnomalyScore      float64 // 0-100, higher = more anomalous
	ReliabilityScore  float64 // 0-100, based on physical consistency
	PredictedFailMode string
	TimeToAnomaly     int
}

// Machine reliability assessment
type MachineReliability struct {
	MachineName         string
	OverallReliability  float64 // 0-100
	OperabilityStatus   string  // "FULLY_OPERATIONAL", "DEGRADED", "MARGINAL", "INOPERABLE"
	SystemConsistency   float64 // How well sensors agree with physics
	CorrelationHealth   float64 // How well correlated sensors are tracking
	StateCoherence      float64 // How well machine state matches sensor readings
	IdentifiedAnomalies []string
	PredictedFailures   []PredictedFailure
	RecommendedActions  []string
	ConfidenceLevel     string
}

type PredictedFailure struct {
	Component     string
	FailureMode   string
	TimeToFailure int
	Probability   float64
	Indicators    []string
}

var systemModel *SystemModel
var machineStates map[string]*OperationalState
var statesMutex sync.RWMutex

func defineSystemModel() {
	// Compressor performance group
	systemModel.SensorGroups["compressor_performance"] = SensorGroup{
		Name:        "compressor_performance",
		Sensors:     []string{"COMP DISCH PRES", "COMP DISCH TEMP", "MOT CURRENT"},
		Correlation: 0.9,
		Description: "Compressor load indicators",
	}

	// Oil system group
	systemModel.SensorGroups["oil_system"] = SensorGroup{
		Name:        "oil_system",
		Sensors:     []string{"COMP OIL PRES", "COMP OIL LVL", "COMP LO SUMP TEMP"},
		Correlation: 0.7,
		Description: "Lubrication system health",
	}

	// Cooling water group
	systemModel.SensorGroups["cooling_water"] = SensorGroup{
		Name:        "cooling_water",
		Sensors:     []string{"CW FLW", "CW IN PRES", "CW PMP DISCH PRES"},
		Correlation: 0.8,
		Description: "Cooling water circulation",
	}

	// Temperature group
	systemModel.SensorGroups["cooling_temps"] = SensorGroup{
		Name:        "cooling_temps",
		Sensors:     []string{"CW IN TEMP", "CW OUT TEMP", "COND SW IN TEMP", "COND SW OUT TEMP"},
		Correlation: 0.75,
		Description: "Heat transfer system",
	}

	// Refrigerant cycle
	systemModel.SensorGroups["refrigerant_cycle"] = SensorGroup{
		Name:        "refrigerant_cycle",
		Sensors:     []string{"REFRGRNT COND LIQ TEMP", "REFRGRNT EVAP LIQ TEMP", "DISCH SATURTN TEMP"},
		Correlation: 0.7,
		Description: "Refrigeration cycle",
	}

	// Define inverse relationships
	systemModel.InverseRelations = append(systemModel.InverseRelations,
		InverseRelation{
			Group1: []string{"COMP DISCH PRES", "COMP DISCH TEMP"},
			Group2: []string{"COMP SUCT PRES"},
		},
		InverseRelation{
			Group1: []string{"CW OUT TEMP"},
			Group2: []string{"CW FLW"},
		},
	)

	// Define causal chains
	systemModel.CausalChains["COMP RUNNING"] = CausalChain{
		Trigger:    "COMP RUNNING",
		Immediate:  []string{"MOT CURRENT", "COMP DISCH PRES"},
		ShortDelay: []string{"COMP DISCH TEMP", "COMP OIL PRES"},
		LongDelay:  []string{"CW OUT TEMP", "COMP LO SUMP TEMP"},
	}
}

// Calculate correlation between sensors in a group
func calculateGroupCorrelation(machineName string, groupSensors []string) float64 {
	if len(groupSensors) < 2 {
		return 1.0
	}

	// Get trend data for all sensors in group
	trends := make([]*TrendAnalysis, 0)
	for _, sensorShort := range groupSensors {
		sensorFull := machineName + ":" + sensorShort
		if limit, exists := operationalLimits[sensorFull]; exists {
			trend := AnalyzeSensorTrend(sensorFull, limit)
			if trend != nil && trend.Confidence != "LOW" {
				trends = append(trends, trend)
			}
		}
	}

	if len(trends) < 2 {
		return 0.5 // Uncertain
	}

	// Check if slopes are similar (should be correlated)
	slopes := make([]float64, len(trends))
	for i, t := range trends {
		slopes[i] = t.Slope
	}

	// Calculate coefficient of variation
	mean := 0.0
	for _, s := range slopes {
		mean += s
	}
	mean /= float64(len(slopes))

	variance := 0.0
	for _, s := range slopes {
		variance += (s - mean) * (s - mean)
	}
	variance /= float64(len(slopes))
	stdDev := math.Sqrt(variance)

	// Low CV = high correlation
	cv := 0.0
	if math.Abs(mean) > 0.001 {
		cv = stdDev / math.Abs(mean)
	}

	// Convert CV to correlation score (0-1)
	correlationScore := math.Max(0, 1.0-cv)

	return correlationScore
}

// Check if inverse relationships are holding
func checkInverseRelationships(machineName string) float64 {
	score := 1.0
	checked := 0

	for _, relation := range systemModel.InverseRelations {
		// Get trends for group1 sensors
		group1Slopes := make([]float64, 0)
		for _, sensorShort := range relation.Group1 {
			sensorFull := machineName + ":" + sensorShort
			if limit, exists := operationalLimits[sensorFull]; exists {
				trend := AnalyzeSensorTrend(sensorFull, limit)
				if trend != nil && trend.Confidence != "LOW" {
					group1Slopes = append(group1Slopes, trend.Slope)
				}
			}
		}

		// Get trends for group2 sensors
		group2Slopes := make([]float64, 0)
		for _, sensorShort := range relation.Group2 {
			sensorFull := machineName + ":" + sensorShort
			if limit, exists := operationalLimits[sensorFull]; exists {
				trend := AnalyzeSensorTrend(sensorFull, limit)
				if trend != nil && trend.Confidence != "LOW" {
					group2Slopes = append(group2Slopes, trend.Slope)
				}
			}
		}

		if len(group1Slopes) > 0 && len(group2Slopes) > 0 {
			// Calculate average slopes
			avg1 := 0.0
			for _, s := range group1Slopes {
				avg1 += s
			}
			avg1 /= float64(len(group1Slopes))

			avg2 := 0.0
			for _, s := range group2Slopes {
				avg2 += s
			}
			avg2 /= float64(len(group2Slopes))

			// Check if they have opposite signs
			if (avg1 > 0 && avg2 < 0) || (avg1 < 0 && avg2 > 0) {
				score += 1.0 // Correct inverse relationship
			} else if math.Abs(avg1) < 0.001 && math.Abs(avg2) < 0.001 {
				score += 0.5 // Both stable, acceptable
			}
			// else: incorrect relationship, no points

			checked++
		}
	}

	if checked > 0 {
		return score / float64(checked+1)
	}
	return 0.5 // No data
}

// Detect operational state from sensor values
func detectMachineState(machineName string) string {
	// Check if compressor is running
	runningSensor := machineName + ":COMP RUNNING"
	if trendData, exists := sensorTrends[runningSensor]; exists {
		trendData.mutex.RLock()
		if len(trendData.Values) > 0 {
			currentValue := trendData.Values[len(trendData.Values)-1]
			trendData.mutex.RUnlock()

			if currentValue > 0.5 {
				// Check motor current to confirm
				currentSensor := machineName + ":MOT CURRENT"
				if currentTrend, exists := sensorTrends[currentSensor]; exists {
					currentTrend.mutex.RLock()
					if len(currentTrend.Values) > 0 {
						current := currentTrend.Values[len(currentTrend.Values)-1]
						currentTrend.mutex.RUnlock()

						if current > 20 {
							return "RUNNING"
						} else if current > 5 {
							return "STARTING"
						}
					} else {
						currentTrend.mutex.RUnlock()
					}
				}
				return "RUNNING"
			} else {
				return "STOPPED"
			}
		} else {
			trendData.mutex.RUnlock()
		}
	}

	return "UNKNOWN"
}

// Advanced machine reliability analysis
func AnalyzeMachineReliability(machineName string, machineStats *MachineStatus) *MachineReliability {
	reliability := &MachineReliability{
		MachineName:         machineName,
		IdentifiedAnomalies: make([]string, 0),
		PredictedFailures:   make([]PredictedFailure, 0),
		RecommendedActions:  make([]string, 0),
	}

	// Detect current operational state
	_ = detectMachineState(machineName)

	// Calculate system consistency (how well sensors agree with physics)
	consistencyScores := make([]float64, 0)

	for groupName, group := range systemModel.SensorGroups {
		correlation := calculateGroupCorrelation(machineName, group.Sensors)
		consistencyScores = append(consistencyScores, correlation*100)

		if correlation < 0.5 {
			reliability.IdentifiedAnomalies = append(reliability.IdentifiedAnomalies,
				"Low correlation in "+groupName)
		}
	}

	// Calculate correlation health
	if len(consistencyScores) > 0 {
		sum := 0.0
		for _, s := range consistencyScores {
			sum += s
		}
		reliability.SystemConsistency = sum / float64(len(consistencyScores))
		reliability.CorrelationHealth = reliability.SystemConsistency
	} else {
		reliability.SystemConsistency = 50.0
		reliability.CorrelationHealth = 50.0
	}

	// Check inverse relationships
	inverseScore := checkInverseRelationships(machineName) * 100
	reliability.StateCoherence = inverseScore

	// Calculate overall reliability score
	reliability.OverallReliability = (reliability.SystemConsistency*0.4 +
		reliability.CorrelationHealth*0.3 +
		reliability.StateCoherence*0.3)

	// Determine operability status
	if reliability.OverallReliability >= 80 {
		reliability.OperabilityStatus = "FULLY_OPERATIONAL"
		reliability.ConfidenceLevel = "HIGH"
	} else if reliability.OverallReliability >= 60 {
		reliability.OperabilityStatus = "DEGRADED"
		reliability.ConfidenceLevel = "MEDIUM"
		reliability.RecommendedActions = append(reliability.RecommendedActions,
			"Monitor system closely - degraded performance detected")
	} else if reliability.OverallReliability >= 40 {
		reliability.OperabilityStatus = "MARGINAL"
		reliability.ConfidenceLevel = "MEDIUM"
		reliability.RecommendedActions = append(reliability.RecommendedActions,
			"Investigate sensor anomalies - system marginally operational")
	} else {
		reliability.OperabilityStatus = "INOPERABLE"
		reliability.ConfidenceLevel = "HIGH"
		reliability.RecommendedActions = append(reliability.RecommendedActions,
			"CRITICAL: System showing signs of failure or sensor malfunction")
	}

	// Identify specific failure modes
	identifyFailureModes(machineName, reliability)

	return reliability
}

// Identify specific failure modes based on sensor patterns
func identifyFailureModes(machineName string, reliability *MachineReliability) {
	// Check for oil system failure
	oilPressure := machineName + ":COMP OIL PRES"
	oilLevel := machineName + ":COMP OIL LVL"

	oilPressureTrend := AnalyzeSensorTrend(oilPressure, operationalLimits[oilPressure])
	oilLevelTrend := AnalyzeSensorTrend(oilLevel, operationalLimits[oilLevel])

	if oilPressureTrend != nil && oilPressureTrend.Slope < -0.01 {
		if oilLevelTrend != nil && oilLevelTrend.Slope < -0.005 {
			reliability.PredictedFailures = append(reliability.PredictedFailures, PredictedFailure{
				Component:     "Oil System",
				FailureMode:   "Oil leak or pump failure",
				TimeToFailure: oilPressureTrend.TimeToCritical,
				Probability:   0.75,
				Indicators:    []string{"Decreasing oil pressure", "Decreasing oil level"},
			})
			reliability.RecommendedActions = append(reliability.RecommendedActions,
				"URGENT: Check for oil leaks and oil pump operation")
		}
	}

	// Check for refrigerant leak
	dischPressure := machineName + ":COMP DISCH PRES"
	suctPressure := machineName + ":COMP SUCT PRES"

	dischTrend := AnalyzeSensorTrend(dischPressure, operationalLimits[dischPressure])
	suctTrend := AnalyzeSensorTrend(suctPressure, operationalLimits[suctPressure])

	if dischTrend != nil && suctTrend != nil {
		if dischTrend.Slope < -0.05 && suctTrend.Slope < -0.03 {
			reliability.PredictedFailures = append(reliability.PredictedFailures, PredictedFailure{
				Component:     "Refrigerant System",
				FailureMode:   "Refrigerant leak",
				TimeToFailure: dischTrend.TimeToCritical,
				Probability:   0.65,
				Indicators:    []string{"Decreasing discharge pressure", "Decreasing suction pressure"},
			})
			reliability.RecommendedActions = append(reliability.RecommendedActions,
				"Check refrigerant charge and inspect for leaks")
		}
	}

	// Check for fouled condenser (high discharge temp with normal pressure)
	dischTemp := machineName + ":COMP DISCH TEMP"
	tempTrend := AnalyzeSensorTrend(dischTemp, operationalLimits[dischTemp])

	if tempTrend != nil && dischTrend != nil {
		if tempTrend.Slope > 0.1 && math.Abs(dischTrend.Slope) < 0.02 {
			reliability.PredictedFailures = append(reliability.PredictedFailures, PredictedFailure{
				Component:     "Condenser",
				FailureMode:   "Fouled condenser or cooling water issue",
				TimeToFailure: tempTrend.TimeToWarning,
				Probability:   0.55,
				Indicators:    []string{"Increasing discharge temperature", "Stable discharge pressure"},
			})
			reliability.RecommendedActions = append(reliability.RecommendedActions,
				"Inspect condenser tubes and cooling water flow")
		}
	}
}
