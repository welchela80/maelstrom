# synthetic_publisher.py
import pika
import csv
import json
import time
import sys
import random
from datetime import datetime

class SyntheticSensorGenerator:
    def __init__(self, limits_file):
        """
        Initialize generator with operational limits from CSV
        """
        self.sensors = {}
        self.machines = {}
        self.load_limits(limits_file)
        self.fault_machine = None
        self.fault_start_time = None
        self.fault_duration = 0
        
    def load_limits(self, limits_file):
        """
        Load sensor limits from CSV
        """
        with open(limits_file, 'r') as f:
            reader = csv.DictReader(f)
            for row in reader:
                sensor_name = row['machineName:sensorName']
                high = float(row['operationalHigh'])
                low = float(row['operationalLow'])
                
                # Parse machine name
                parts = sensor_name.split(':', 1)
                if len(parts) == 2:
                    machine_name = parts[0]
                    sensor_short_name = parts[1]
                else:
                    machine_name = "UNKNOWN"
                    sensor_short_name = sensor_name
                
                # Store sensor info
                self.sensors[sensor_name] = {
                    'machine': machine_name,
                    'name': sensor_short_name,
                    'high': high,
                    'low': low,
                    'current_value': None,
                    'target_percentage': random.uniform(40, 60)  # Start in middle range
                }
                
                # Group by machine
                if machine_name not in self.machines:
                    self.machines[machine_name] = []
                self.machines[machine_name].append(sensor_name)
        
        print(f"Loaded {len(self.sensors)} sensors across {len(self.machines)} machines")
        for machine, sensors in self.machines.items():
            print(f"  {machine}: {len(sensors)} sensors")
    
    def calculate_value_from_percentage(self, sensor_name, target_percentage):
        """
        Calculate sensor value based on target percentage of range
        """
        sensor = self.sensors[sensor_name]
        range_span = sensor['high'] - sensor['low']
        
        if range_span <= 0:
            # No range, return low value
            return sensor['low']
        
        # Calculate value from percentage
        value = sensor['low'] + (range_span * target_percentage / 100.0)
        
        # Add some random noise (±2%)
        noise = random.uniform(-0.02, 0.02) * range_span
        value += noise
        
        # Clamp to reasonable bounds
        value = max(sensor['low'] - range_span * 0.1, value)
        value = min(sensor['high'] + range_span * 0.1, value)
        
        return value
    
    def initialize_sensors(self):
        """
        Initialize all sensors with good starting values
        """
        for sensor_name in self.sensors:
            sensor = self.sensors[sensor_name]
            # Start at target percentage (40-60%, good range)
            sensor['current_value'] = self.calculate_value_from_percentage(
                sensor_name, sensor['target_percentage']
            )
    
    def trigger_fault_scenario(self):
        """
        Randomly trigger a fault scenario on a machine
        """
        # Only trigger new fault if no current fault
        if self.fault_machine is None:
            # 5% chance to trigger fault each reading cycle
            if random.random() < 0.05:
                self.fault_machine = random.choice(list(self.machines.keys()))
                self.fault_start_time = time.time()
                self.fault_duration = random.uniform(30, 120)  # 30-120 seconds
                print(f"\n🔴 FAULT SCENARIO TRIGGERED: {self.fault_machine} - Duration: {self.fault_duration:.0f}s\n")
    
    def update_sensor_values(self):
        """
        Update all sensor values with realistic drift and fault scenarios
        """
        current_time = time.time()
        
        # Check if we should clear fault
        if self.fault_machine and (current_time - self.fault_start_time) > self.fault_duration:
            print(f"\n✅ FAULT SCENARIO CLEARED: {self.fault_machine}\n")
            self.fault_machine = None
            self.fault_start_time = None
        
        for sensor_name in self.sensors:
            sensor = self.sensors[sensor_name]
            
            # Check if this sensor's machine is in fault
            if sensor['machine'] == self.fault_machine:
                # Apply fault logic - drive values toward limits
                fault_progress = (current_time - self.fault_start_time) / self.fault_duration
                
                # Randomly choose to go high or low (stay consistent per fault)
                if not hasattr(sensor, 'fault_direction'):
                    sensor['fault_direction'] = random.choice(['high', 'low'])
                
                if sensor['fault_direction'] == 'high':
                    # Drive toward high limit and beyond
                    target = 80 + (fault_progress * 40)  # 80% -> 120%
                else:
                    # Drive toward low limit and beyond
                    target = 20 - (fault_progress * 30)  # 20% -> -10%
                
                sensor['target_percentage'] = target
            else:
                # Normal operation - random walk with tendency to stay in good range
                current_pct = sensor['target_percentage']
                
                # Drift toward middle (50%) with some randomness
                drift = random.uniform(-2, 2)  # Random walk
                center_pull = (50 - current_pct) * 0.05  # Pull toward center
                
                sensor['target_percentage'] += drift + center_pull
                
                # Keep mostly in good range (20-80%)
                sensor['target_percentage'] = max(15, min(85, sensor['target_percentage']))
                
                # Clear fault direction if it exists
                if hasattr(sensor, 'fault_direction'):
                    delattr(sensor, 'fault_direction')
            
            # Calculate actual value from target percentage
            sensor['current_value'] = self.calculate_value_from_percentage(
                sensor_name, sensor['target_percentage']
            )
    
    def generate_reading(self):
        """
        Generate a complete sensor reading for all sensors
        """
        self.update_sensor_values()
        self.trigger_fault_scenario()
        
        readings = {}
        for sensor_name, sensor in self.sensors.items():
            # Format value appropriately
            if sensor['current_value'] is not None:
                # Check if this looks like a binary/state sensor (0-1 range)
                if sensor['high'] == 1.0 and sensor['low'] == 0.0:
                    # Binary sensor - round to 0 or 1
                    readings[sensor_name] = str(int(round(sensor['current_value'])))
                else:
                    # Continuous sensor
                    readings[sensor_name] = f"{sensor['current_value']:.2f}"
        
        return readings

def publish_synthetic_data(limits_file, queue_name='sensor_readings', interval=1.0):
    """
    Generate and publish synthetic sensor data
    """
    
    # Initialize generator
    print("Initializing synthetic data generator...")
    generator = SyntheticSensorGenerator(limits_file)
    generator.initialize_sensors()
    
    # Connect to RabbitMQ
    try:
        credentials = pika.PlainCredentials('guest', 'guest')
        parameters = pika.ConnectionParameters(
            host='localhost',
            port=5672,
            virtual_host='/',
            credentials=credentials,
            connection_attempts=3,
            retry_delay=2
        )
        
        print("Attempting to connect to RabbitMQ...")
        connection = pika.BlockingConnection(parameters)
        channel = connection.channel()
        print("Connected successfully!")
        
    except Exception as e:
        print(f"Failed to connect to RabbitMQ: {e}")
        sys.exit(1)
    
    # Declare queue
    channel.queue_declare(queue=queue_name, durable=True)
    
    print(f"\nStarting synthetic data generation at {1/interval}Hz")
    print("Press CTRL+C to stop\n")
    
    message_count = 0
    
    try:
        while True:
            # Generate readings
            readings = generator.generate_reading()
            
            # Create message
            message = {
                'timestamp': datetime.utcnow().isoformat(),
                'source': 'synthetic_generator',
                'readings': readings
            }
            
            # Publish to RabbitMQ
            channel.basic_publish(
                exchange='',
                routing_key=queue_name,
                body=json.dumps(message),
                properties=pika.BasicProperties(
                    delivery_mode=2,
                )
            )
            
            message_count += 1
            if message_count % 10 == 0:
                print(f"Published {message_count} synthetic readings...")
            
            # Wait for interval
            time.sleep(interval)
            
    except KeyboardInterrupt:
        print("\n\nStopping synthetic data generator...")
    finally:
        connection.close()
        print(f"Published {message_count} synthetic readings. Connection closed.")

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: python synthetic_publisher.py <limits_csv> [queue_name] [interval]")
        print("  limits_csv: Path to sensor operational limits CSV file")
        print("  queue_name: RabbitMQ queue name (default: sensor_readings)")
        print("  interval: Time between messages in seconds (default: 1.0)")
        sys.exit(1)
    
    limits_file = sys.argv[1]
    queue_name = sys.argv[2] if len(sys.argv) > 2 else 'sensor_readings'
    interval = float(sys.argv[3]) if len(sys.argv) > 3 else 1.0
    
    publish_synthetic_data(limits_file, queue_name, interval)