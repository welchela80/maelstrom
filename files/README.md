# Predictive Analytics Engine - Technical Documentation

## Overview

The analytics engine performs real-time trend analysis and predictive modeling on sensor data streams to identify potential equipment failures before they occur. It uses linear regression and statistical analysis to track sensor behavior over time and forecast future states.

---

## Core Algorithm: Linear Regression Analysis

### What is Linear Regression?

Linear regression finds the "line of best fit" through a series of data points. For sensor data, this line represents the trend over time. The equation is:

```
y = mx + b
```

Where:
- **y** = predicted sensor value
- **m** = slope (rate of change per second)
- **x** = time (in seconds)
- **b** = intercept (starting value)

### Example

If a temperature sensor reads:
- At 0 seconds: 50°C
- At 60 seconds: 55°C
- At 120 seconds: 60°C

The regression calculates:
- **Slope (m)**: 0.083°C/second (5°C change over 60 seconds)
- **Intercept (b)**: 50°C
- **Prediction**: In 5 minutes (300s), temperature will be: `y = 0.083 × 300 + 50 = 75°C`

---

## How the Algorithm Works

### 1. Data Collection (Real-Time)

```
For each sensor reading:
  ├── Parse sensor value (convert to float)
  ├── Get current timestamp
  ├── Store in historical buffer (last 100 points)
  └── Maintain FIFO queue (oldest data drops off)
```

**Why 100 points?**
- Provides ~100 seconds of history at 1Hz sampling
- Balances memory usage vs. trend accuracy
- Enough data for meaningful statistical analysis

### 2. Least Squares Regression

The algorithm uses the **Least Squares Method** to calculate the best-fit line:

```
Slope (m) = [n·Σ(xy) - Σx·Σy] / [n·Σ(x²) - (Σx)²]
Intercept (b) = [Σy - m·Σx] / n
```

Where:
- **n** = number of data points
- **Σxy** = sum of (time × value) products
- **Σx** = sum of all time values
- **Σy** = sum of all sensor values
- **Σx²** = sum of squared time values

**Implementation:**
```go
for i := 0; i < len(x); i++ {
    sumX += x[i]
    sumY += y[i]
    sumXY += x[i] * y[i]
    sumXX += x[i] * x[i]
}

slope = (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
intercept = (sumY - slope*sumX) / n
```

### 3. R-Squared Calculation (Goodness of Fit)

**R²** measures how well the line fits the data (0 = terrible, 1 = perfect):

```
R² = 1 - (SS_residual / SS_total)
```

Where:
- **SS_total** = Σ(y - ȳ)² (total variance from mean)
- **SS_residual** = Σ(y - ŷ)² (variance from predicted line)

**Interpretation:**
- **R² > 0.8**: HIGH confidence - trend is very clear
- **R² 0.5-0.8**: MEDIUM confidence - trend exists but noisy
- **R² < 0.5**: LOW confidence - data too erratic for reliable prediction

**Example:**
If temperature fluctuates randomly between 49-51°C:
- R² ≈ 0.2 (LOW confidence - no clear trend)

If temperature steadily increases 49→50→51→52°C:
- R² ≈ 0.99 (HIGH confidence - strong trend)

### 4. Trend Direction Classification

```
if |slope| < 0.001:
    trend = "STABLE"      // Nearly flat line
else if slope > 0:
    trend = "INCREASING"  // Rising
else:
    trend = "DECREASING"  // Falling
```

**Why 0.001 threshold?**
Very small slopes indicate stable behavior rather than actual trends. This prevents flagging minor fluctuations as meaningful trends.

---

## Predictive Features

### 1. Future Value Predictions

Using the regression line, predict values at future time points:

```
Prediction(t) = slope × t + intercept
```

**Implemented predictions:**
- **5 minutes ahead**: `slope × (current_time + 300) + intercept`
- **10 minutes ahead**: `slope × (current_time + 600) + intercept`

**Example:**
```
Current: 55°C at t=120s
Slope: 0.083°C/s
Intercept: 50°C

5-min prediction: 0.083 × (120 + 300) + 50 = 84.86°C
```

### 2. Time to Warning Zone

Calculates when sensor will enter warning range (>80% or <20% of operational range):

```
For increasing trend (slope > 0):
    warning_threshold = low_limit + (range × 0.8)
    time_to_warning = (warning_threshold - current_value) / slope

For decreasing trend (slope < 0):
    warning_threshold = low_limit + (range × 0.2)
    time_to_warning = (current_value - warning_threshold) / |slope|
```

**Example:**
```
Sensor range: 0-100°C
Current value: 60°C
Slope: 0.1°C/s (increasing)
Warning threshold: 0 + (100 × 0.8) = 80°C

Time to warning = (80 - 60) / 0.1 = 200 seconds (~3 minutes)
```

### 3. Time to Critical (Limit Exceeded)

Calculates when sensor will exceed operational limits:

```
For increasing trend:
    time_to_critical = (high_limit - current_value) / slope

For decreasing trend:
    time_to_critical = (current_value - low_limit) / |slope|
```

**Example:**
```
High limit: 100°C
Current: 85°C
Slope: 0.2°C/s

Time to critical = (100 - 85) / 0.2 = 75 seconds
```

---

## Health Score Calculation

The health score (0-100) combines position in range and trend direction:

### Base Score: 100

### Penalties Applied:

**1. Position Penalty**
```
if current_percentage < 20%:
    penalty = (20 - current_percentage) × 2
if current_percentage > 80%:
    penalty = (current_percentage - 80) × 2
```

**2. Adverse Trend Penalty**
```
if (slope > 0 AND current_percentage > 50):
    penalty = |slope| × 100    // Rising when already high
if (slope < 0 AND current_percentage < 50):
    penalty = |slope| × 100    // Falling when already low
```

**3. Stability Bonus**
```
if trend == "STABLE":
    bonus = +10
```

**Final Score:**
```
health_score = max(0, min(100, base - penalties + bonus))
```

### Health Score Interpretation

- **90-100**: Excellent - sensor operating in ideal range with stable trend
- **70-89**: Good - acceptable range and trend
- **50-69**: Fair - approaching warning zone or adverse trend
- **30-49**: Poor - in warning zone or trending toward critical
- **0-29**: Critical - outside limits or imminent failure

**Example Calculation:**
```
Sensor: 85% of range
Slope: +0.05 (increasing)
Trend: INCREASING

Base: 100
Position penalty: (85 - 80) × 2 = -10
Adverse trend penalty: 0.05 × 100 = -5
Final health score: 100 - 10 - 5 = 85 (GOOD)
```

---

## Machine-Level Aggregation

### Sensor Risk Assessment

```
For each sensor in machine:
    if time_to_warning > 0 AND time_to_warning < 600s:
        sensors_at_risk++
```

Counts sensors that will enter warning zone within 10 minutes.

### Overall Machine Trend

```
degrading_count = count(sensors with health < 60)
improving_count = count(sensors with health > 80)
stable_count = remaining sensors

if degrading_count > total/3:
    machine_trend = "DEGRADING"
else if improving_count > total/3:
    machine_trend = "IMPROVING"
else:
    machine_trend = "STABLE"
```

### Machine Health Score

```
machine_health = average(all sensor health scores)
```

### Estimated Time to Failure

```
failure_time = minimum(time_to_critical across all sensors)
```

Takes the **most critical** sensor's time to failure as the machine's estimated failure time.

---

## Output Metrics

### Per-Sensor Metrics (Console Only)

Not exposed in JSON, but logged for debugging:
- Slope (rate of change)
- R² (confidence in trend)
- 5-min and 10-min predictions
- Time to warning/critical

### Per-Machine Metrics (Console + JSON)

**Console Output:**
```
GTM 2A    Status: GOOD              | Running: RUNNING             | Avg: 55.23%
          Trend: STABLE             | Health: 87.5 | Risk: 2 sensors | Fail: 15m | Conf: HIGH
```

**JSON Output:**
```json
{
  "GTM 2A": {
    "status": "GOOD",
    "running": "RUNNING",
    "avg_percentage": 55.23,
    "overall_trend": "STABLE",
    "health_score": 87.5,
    "sensors_at_risk": 2,
    "estimated_fail_time": 900,
    "trend_confidence": "HIGH"
  }
}
```

---

## Algorithm Strengths

✅ **Real-time**: Updates every 10 seconds with new data
✅ **Predictive**: Forecasts future states, not just current status
✅ **Statistical**: Uses proven regression methods
✅ **Confidence-aware**: Reports uncertainty (R²)
✅ **Scalable**: O(n) complexity, handles hundreds of sensors
✅ **Memory-efficient**: Fixed 100-point buffer per sensor

---

## Algorithm Limitations

⚠️ **Linear assumption**: Assumes trends are linear (may miss exponential failures)
⚠️ **Short history**: 100 seconds of data may miss long-term patterns
⚠️ **No seasonality**: Doesn't account for cyclical patterns
⚠️ **No multivariate**: Analyzes sensors independently, misses correlations
⚠️ **Outlier sensitivity**: Single bad data point can skew regression

---

## Potential Enhancements

### 1. Exponential Smoothing
Weight recent data more heavily than old data:
```
weighted_value[i] = alpha × actual[i] + (1-alpha) × predicted[i]
```

### 2. Polynomial Regression
Fit curves instead of lines for non-linear trends:
```
y = ax² + bx + c
```

### 3. Moving Average
Smooth noisy data before regression:
```
smoothed[i] = average(last_N_values)
```

### 4. Multi-Sensor Correlation
Detect patterns across related sensors:
```
if (temp↑ AND pressure↑ AND flow↓):
    alert("Pump degradation pattern")
```

### 5. Anomaly Detection
Flag sudden deviations from trend:
```
if |actual - predicted| > 3 × std_deviation:
    alert("Anomaly detected")
```

---

## Example Scenario

### Scenario: Failing Air Compressor

**Initial State (t=0s):**
```
Pressure: 45 PSI (45% of 0-100 range)
Slope: 0.0 (stable)
Health: 95 (good, centered in range)
```

**After 2 minutes (t=120s):**
```
Pressure: 50 PSI → 48 PSI → 46 PSI → 43 PSI (declining)
Slope: -0.058 PSI/s
R²: 0.92 (high confidence)
Prediction (5min): 26 PSI
Time to warning (20%): 68 seconds
Health: 52 (fair, adverse trend)
```

**After 5 minutes (t=300s):**
```
Pressure: 28 PSI (28% of range, below ideal)
Slope: -0.062 PSI/s
Prediction (5min): 9 PSI
Time to critical: 75 seconds
Health: 25 (critical)
Status: DEGRADING
Alert: "Compressor failure imminent - 75s to shutdown"
```

**Result:**
The algorithm detected the failing compressor **5 minutes** before it reached critical pressure, giving operators time to:
- Switch to backup compressor
- Schedule maintenance
- Prevent equipment damage

---

## Summary

The analytics engine transforms raw sensor streams into actionable intelligence by:

1. **Tracking** historical behavior (100-point buffer)
2. **Analyzing** trends (linear regression)
3. **Predicting** future states (extrapolation)
4. **Scoring** equipment health (multi-factor analysis)
5. **Alerting** on degradation (time-to-failure calculations)

This enables **predictive maintenance** rather than reactive repairs, reducing downtime and preventing catastrophic failures.