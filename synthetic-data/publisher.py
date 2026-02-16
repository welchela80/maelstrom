# synthetic_publisher_enhanced.py
"""
Enhanced synthetic data generator that models physical sensor relationships
Based on ship system hierarchy JSON structure
"""
import pika
import csv
import json
import time
import sys
import os
import random
import math
from datetime import datetime
from collections import defaultdict

class PhysicalSystemModel:
    """Models physical relationships between sensors based on thermodynamic principles"""
    
    def __init__(self):
        # Sensor relationship groups - sensors that should move together
        self.sensor_groups = {
            # Compressor relationships
            'compressor_performance': {
                'sensors': ['COMP DISCH PRES', 'COMP DISCH TEMP', 'MOT CURRENT'],
                'correlation': 0.9,  # High correlation
                'description': 'As load increases, discharge pressure, temp, and current all rise'
            },
            'compressor_oil_system': {
                'sensors': ['COMP OIL PRES', 'COMP OIL LVL', 'COMP LO SUMP TEMP'],
                'correlation': 0.7,
                'description': 'Oil pressure, level, and temperature are related'
            },
            'suction_conditions': {
                'sensors': ['COMP SUCT PRES', 'COMP SUCT TEMP', 'SUCT SATURTN TEMP'],
                'correlation': 0.85,
                'description': 'Suction pressure and temperature move together'
            },
            
            # Cooling water system
            'cooling_water_flow': {
                'sensors': ['CW FLW', 'CW IN PRES', 'CW PMP DISCH PRES'],
                'correlation': 0.8,
                'description': 'Flow and pressure are directly related'
            },
            'cooling_water_temps': {
                'sensors': ['CW IN TEMP', 'CW OUT TEMP', 'COND SW IN TEMP', 'COND SW OUT TEMP'],
                'correlation': 0.75,
                'description': 'Cooling water temperatures track together'
            },
            
            # Refrigerant cycle
            'refrigerant_cycle': {
                'sensors': ['REFRGRNT COND LIQ TEMP', 'REFRGRNT EVAP LIQ TEMP', 'DISCH SATURTN TEMP'],
                'correlation': 0.7,
                'description': 'Refrigerant temperatures are thermodynamically linked'
            },
            
            # Control valves
            'valve_positions': {
                'sensors': ['CTRL VLV POSN', 'VGD POSN', 'PRV POSN'],
                'correlation': 0.6,
                'description': 'Valve positions may adjust together for load changes'
            }
        }
        
        # Inverse relationships (when one goes up, the other goes down)
        self.inverse_relationships = [
            (['COMP DISCH PRES', 'COMP DISCH TEMP'], ['COMP SUCT PRES']),  # High discharge means low suction
            (['CW OUT TEMP'], ['CW FLW']),  # Higher flow means lower outlet temp
            (['MOT CURRENT'], ['COMP OIL LVL']),  # Higher load may reduce oil level slightly
        ]
        
        # Cause-and-effect delays (sensor A affects sensor B with a delay)
        self.time_delays = {
            'COMP RUNNING': {  # When compressor starts
                'immediate': ['MOT CURRENT', 'COMP DISCH PRES'],  # 0-5 seconds
                'short_delay': ['COMP DISCH TEMP', 'COMP OIL PRES'],  # 5-30 seconds
                'long_delay': ['CW OUT TEMP', 'COMP LO SUMP TEMP']  # 30-60 seconds
            },
            'CW FLW': {  # When flow changes
                'short_delay': ['CW OUT TEMP', 'COND SW OUT TEMP'],
                'long_delay': ['REFRGRNT COND LIQ TEMP']
            }
        }
        
        # Operational state machines
        self.machine_states = {}  # Will track current state of each machine
        
    def get_correlation_factor(self, sensor1, sensor2):
        """Get correlation factor between two sensors"""
        for group_name, group_info in self.sensor_groups.items():
            if sensor1 in group_info['sensors'] and sensor2 in group_info['sensors']:
                return group_info['correlation']
        return 0.0  # No correlation
    
    def is_inverse_related(self, sensor1, sensor2):
        """Check if two sensors have inverse relationship"""
        for group1, group2 in self.inverse_relationships:
            if (sensor1 in group1 and sensor2 in group2) or (sensor1 in group2 and sensor2 in group1):
                return True
        return False

class EnhancedSensorGenerator:
    def __init__(self, limits_file, hierarchy_file=None):
        self.sensors = {}
        self.machines = {}
        self.system_model = PhysicalSystemModel()
        
        # Track machine state over time
        self.machine_state_history = defaultdict(lambda: {'values': {}, 'time': time.time()})
        
        # Equipment rotation schedules - will be auto-populated
        self.equipment_rotation = {}
        
        # Machine operational modes
        self.machine_modes = {}  # Will track: RUNNING, STANDBY, STOPPED, STARTING, STOPPING
        
        # Now load limits
        self.load_limits(limits_file)
        
        if hierarchy_file:
            self.load_hierarchy(hierarchy_file)
        
    def load_limits(self, limits_file):
        """Load sensor limits from CSV"""
        with open(limits_file, 'r', newline='') as f:
            reader = csv.DictReader(f)
            for row in reader:
                sensor_name = row['machineName:sensorName'].strip()
                system = row.get('system', '').strip()
                high = float(row['operationalHigh'])
                low = float(row['operationalLow'])
                
                # Parse machine and sensor names
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
                    'system': system,
                    'name': sensor_short_name,
                    'high': high,
                    'low': low,
                    'current_value': None,
                    'target_percentage': random.uniform(40, 60),  # Start in good range
                    'rate_of_change': 0.0,  # Rate of change per second
                    'last_update': time.time()
                }
                
                # Group by machine
                if machine_name not in self.machines:
                    self.machines[machine_name] = []
                self.machines[machine_name].append(sensor_name)
        
        print(f"Loaded {len(self.sensors)} sensors across {len(self.machines)} machines")
        
        # Auto-detect machine groups by finding machines with numbers (e.g., "LPAC 1", "LPAC 2", "LPAC 3")
        # Group machines by their base name
        machine_groups = {}
        for machine_name in self.machines.keys():
            # Try to extract base name (remove trailing numbers and letters)
            import re
            # Match patterns like "LPAC 1", "GTM 1A", "FP 6", etc.
            match = re.match(r'^(.*?)\s*[\d]+[A-Z]*$', machine_name)
            if match:
                base_name = match.group(1).strip()
                if base_name not in machine_groups:
                    machine_groups[base_name] = []
                machine_groups[base_name].append(machine_name)
        
        # Set ALL machines to RUNNING by default
        for machine_name in self.machines.keys():
            self.machine_modes[machine_name] = 'RUNNING'
        
        # Apply rotation to detected groups
        for base_name, machines in machine_groups.items():
            if len(machines) <= 1:
                continue  # Skip groups with only 1 machine
            
            # Determine how many should run
            if len(machines) == 2:
                num_running = 1  # 1 of 2
            elif len(machines) == 3:
                num_running = 1  # 1 of 3
            else:  # 4 or more
                num_running = len(machines) // 2  # Half
            
            # Create rotation config
            self.equipment_rotation[base_name] = {
                'machines': sorted(machines),
                'max_running': num_running,
                'min_running': num_running,
                'rotation_interval': 3600,  # Rotate every hour
                'last_rotation': time.time(),
                'running_machines': sorted(machines)[:num_running]
            }
            
            # Set states
            for machine in machines:
                if machine in self.equipment_rotation[base_name]['running_machines']:
                    self.machine_modes[machine] = 'RUNNING'
                else:
                    self.machine_modes[machine] = 'STANDBY'
            
            print(f"\n{base_name}: {num_running} of {len(machines)} machines running")
            print(f"  Running: {self.equipment_rotation[base_name]['running_machines']}")
            print(f"  Standby: {[m for m in machines if m not in self.equipment_rotation[base_name]['running_machines']]}")
        
        # Debug: show all machine modes
        print(f"\nMachine operational modes:")
        for machine, mode in sorted(self.machine_modes.items()):
            print(f"  {machine}: {mode}")
    
    def load_hierarchy(self, hierarchy_file):
        """Load system hierarchy to understand sensor relationships"""
        try:
            with open(hierarchy_file, 'r') as f:
                hierarchy = json.load(f)
                # Extract sensor relationships from hierarchy
                # This could be enhanced to parse the JSON structure
                print(f"Loaded hierarchy from {hierarchy_file}")
        except Exception as e:
            print(f"Could not load hierarchy: {e}")
    
    def calculate_value_from_percentage(self, sensor_name, target_percentage):
        """Calculate sensor value based on target percentage of range"""
        sensor = self.sensors[sensor_name]
        range_span = sensor['high'] - sensor['low']
        
        if range_span <= 0:
            return sensor['low']
        
        # Calculate value from percentage
        value = sensor['low'] + (range_span * target_percentage / 100.0)
        
        # Add some random noise (±1%)
        noise = random.uniform(-0.01, 0.01) * range_span
        value += noise
        
        # Clamp to reasonable bounds
        value = max(sensor['low'] - range_span * 0.1, value)
        value = min(sensor['high'] + range_span * 0.1, value)
        
        return value
    
    def apply_correlation(self, machine_name, primary_sensor, delta_percentage):
        """Apply correlated changes to related sensors"""
        machine_sensors = self.machines.get(machine_name, [])
        
        for sensor_name in machine_sensors:
            if sensor_name == primary_sensor:
                continue
            
            # Get short sensor names for correlation lookup
            primary_short = primary_sensor.split(':')[-1]
            sensor_short = sensor_name.split(':')[-1]
            
            # Check correlation
            correlation = self.system_model.get_correlation_factor(primary_short, sensor_short)
            
            if correlation > 0:
                # Apply correlated change
                if self.system_model.is_inverse_related(primary_short, sensor_short):
                    # Inverse relationship
                    correlated_delta = -delta_percentage * correlation
                else:
                    # Direct relationship
                    correlated_delta = delta_percentage * correlation
                
                # Apply the change
                sensor = self.sensors[sensor_name]
                sensor['target_percentage'] += correlated_delta * random.uniform(0.8, 1.2)  # Add some variance
                
                # Keep in bounds
                sensor['target_percentage'] = max(10, min(90, sensor['target_percentage']))
    
    def rotate_equipment(self):
        """Rotate equipment on/off based on schedules"""
        current_time = time.time()
        
        for system_name, rotation_config in self.equipment_rotation.items():
            if not rotation_config['machines']:
                continue
            
            # Check if it's time to rotate
            time_since_rotation = current_time - rotation_config['last_rotation']
            
            # Random chance to rotate (5% per update, or forced after interval)
            should_rotate = (time_since_rotation > rotation_config['rotation_interval'] or 
                           random.random() < 0.01)
            
            if should_rotate and len(rotation_config['machines']) > rotation_config['min_running']:
                # Decide whether to add or remove a machine
                num_running = len(rotation_config['running_machines'])
                
                if num_running < rotation_config['max_running'] and random.random() < 0.3:
                    # Start another machine (30% chance)
                    stopped_machines = [m for m in rotation_config['machines'] 
                                       if m not in rotation_config['running_machines']]
                    if stopped_machines:
                        machine_to_start = random.choice(stopped_machines)
                        print(f"\n🔵 {system_name}: Starting {machine_to_start}")
                        self.machine_modes[machine_to_start] = 'STARTING'
                        rotation_config['running_machines'].append(machine_to_start)
                        rotation_config['last_rotation'] = current_time
                
                elif num_running > rotation_config['min_running'] and random.random() < 0.2:
                    # Stop a machine (20% chance)
                    machine_to_stop = random.choice(rotation_config['running_machines'])
                    print(f"\n🔴 {system_name}: Stopping {machine_to_stop}")
                    self.machine_modes[machine_to_stop] = 'STOPPING'
                    rotation_config['running_machines'].remove(machine_to_stop)
                    rotation_config['last_rotation'] = current_time
    
    def get_sensor_value_for_mode(self, sensor_name, mode):
        """Calculate sensor value based on machine operational mode"""
        sensor = self.sensors[sensor_name]
        sensor_short = sensor['name']
        
        if mode == 'STOPPED' or mode == 'STANDBY':
            # Machine is off or on standby - sensors at low limit (not below)
            if 'RUNNING' in sensor_short or 'STATUS ON' in sensor_short:
                return 0.0
            elif 'CURRENT' in sensor_short or 'MOT' in sensor_short:
                return sensor['low']  # At minimum
            else:
                # All other sensors at exactly the low limit (not below)
                return sensor['low']
        
        elif mode == 'STARTING':
            # Machine is starting up - values ramping
            if 'RUNNING' in sensor_short or 'STATUS ON' in sensor_short:
                return 1.0
            elif 'CURRENT' in sensor_short:
                # High inrush current during start
                return sensor['low'] + (sensor['high'] - sensor['low']) * 0.7
            elif 'PRES' in sensor_short:
                # Pressures building
                return sensor['low'] + (sensor['high'] - sensor['low']) * 0.3
            else:
                # Use normal calculation
                return self.calculate_value_from_percentage(sensor_name, sensor['target_percentage'])
        
        elif mode == 'STOPPING':
            # Machine is shutting down - values declining
            if 'RUNNING' in sensor_short or 'STATUS ON' in sensor_short:
                return 0.0
            elif 'CURRENT' in sensor_short:
                # Current dropping
                return sensor['low'] + (sensor['high'] - sensor['low']) * 0.2
            elif 'PRES' in sensor_short:
                # Pressures falling
                return sensor['low'] + (sensor['high'] - sensor['low']) * 0.3
            else:
                return self.calculate_value_from_percentage(sensor_name, sensor['target_percentage'] * 0.5)
        
        elif mode == 'RUNNING':
            # Normal operation - use physics-based calculation
            return self.calculate_value_from_percentage(sensor_name, sensor['target_percentage'])
        
        else:
            # Unknown mode - use normal calculation
            return self.calculate_value_from_percentage(sensor_name, sensor['target_percentage'])
    
    def update_machine_modes(self):
        """Update machine operational modes (transitions)"""
        for machine_name, mode in list(self.machine_modes.items()):
            if mode == 'STARTING':
                # Transition to RUNNING after startup (10% chance per update)
                if random.random() < 0.1:
                    self.machine_modes[machine_name] = 'RUNNING'
                    print(f"✅ {machine_name}: Now RUNNING")
            
            elif mode == 'STOPPING':
                # Transition to STANDBY after shutdown (10% chance per update)
                if random.random() < 0.1:
                    self.machine_modes[machine_name] = 'STANDBY'
                    print(f"⏸️  {machine_name}: Now STANDBY")
    
    def simulate_machine_operation(self, machine_name):
        """Update all sensor values with physical relationships and equipment rotation"""
        current_time = time.time()
        
        # Rotate equipment on schedule
        self.rotate_equipment()
        
        # Update machine mode transitions
        self.update_machine_modes()
        
        # Simulate each machine's operation
        for machine_name in self.machines.keys():
            self.simulate_machine_operation(machine_name)
        
        # Update all sensor values based on machine modes
        for sensor_name, sensor in self.sensors.items():
            machine_name = sensor['machine']
            mode = self.machine_modes.get(machine_name, 'RUNNING')
            
            # Calculate value based on mode
            sensor['current_value'] = self.get_sensor_value_for_mode(sensor_name, mode)
            sensor['last_update'] = current_time
    
    def update_sensor_values(self):
        """Update all sensor values with physical relationships and equipment rotation"""
        current_time = time.time()
        
        # Rotate equipment on schedule
        self.rotate_equipment()
        
        # Update machine mode transitions
        self.update_machine_modes()
        
        # Simulate each machine's operation
        for machine_name in self.machines.keys():
            self.simulate_machine_operation(machine_name)
        
        # Update all sensor values based on machine modes
        for sensor_name, sensor in self.sensors.items():
            machine_name = sensor['machine']
            mode = self.machine_modes.get(machine_name, 'RUNNING')
            
            # Calculate value based on mode
            sensor['current_value'] = self.get_sensor_value_for_mode(sensor_name, mode)
            sensor['last_update'] = current_time
    
    def simulate_machine_operation(self, machine_name):
        """Simulate realistic machine operation with correlated sensor movements"""
        machine_sensors = self.machines.get(machine_name, [])
        mode = self.machine_modes.get(machine_name, 'RUNNING')
        
        # Only apply operational changes if machine is RUNNING
        if mode != 'RUNNING':
            return
        
        # Decide on operational change (5% chance per update)
        if random.random() < 0.05:
            # Pick a primary driver sensor
            driver_candidates = [s for s in machine_sensors if 'RUNNING' in s or 'CURRENT' in s or 'FLW' in s]
            if driver_candidates:
                primary_sensor = random.choice(driver_candidates)
                
                # Decide on change magnitude
                delta_percentage = random.uniform(-5, 5)  # ±5% change
                
                # Apply change to primary sensor
                self.sensors[primary_sensor]['target_percentage'] += delta_percentage
                
                # Apply correlated changes to related sensors
                self.apply_correlation(machine_name, primary_sensor, delta_percentage)
    
    def generate_reading(self):
        """Generate a complete sensor reading"""
        self.update_sensor_values()
        
        readings = {}
        for sensor_name, sensor in self.sensors.items():
            if sensor['current_value'] is not None:
                # Format based on sensor type
                if sensor['high'] == 1.0 and sensor['low'] == 0.0:
                    # Binary sensor
                    readings[sensor_name] = str(int(round(sensor['current_value'])))
                else:
                    # Continuous sensor
                    readings[sensor_name] = f"{sensor['current_value']:.2f}"
        
        return readings

def publish_synthetic_data(limits_file, hierarchy_file=None, queue_name='sensor_readings', interval=1.0):
    """Generate and publish enhanced synthetic sensor data"""
    
    print("Initializing enhanced synthetic data generator with physical models...")
    generator = EnhancedSensorGenerator(limits_file, hierarchy_file)
    
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
    
    channel.queue_declare(queue=queue_name, durable=True)
    
    print(f"\nStarting enhanced synthetic data generation at {1/interval}Hz")
    print("Physical relationships and correlations are modeled")
    print("Press CTRL+C to stop\n")
    
    message_count = 0
    
    try:
        while True:
            readings = generator.generate_reading()
            
            message = {
                'timestamp': datetime.utcnow().isoformat(),
                'source': 'enhanced_synthetic_generator',
                'readings': readings
            }
            
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
                print(f"Published {message_count} enhanced synthetic readings...")
            
            time.sleep(interval)
            
    except KeyboardInterrupt:
        print("\n\nStopping enhanced synthetic data generator...")
    finally:
        connection.close()
        print(f"Published {message_count} synthetic readings. Connection closed.")

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: python synthetic_publisher_enhanced.py <limits_csv> [hierarchy_json] [queue_name] [interval]")
        print("  limits_csv: Path to sensor operational limits CSV file")
        print("  hierarchy_json: (Optional) Path to system hierarchy JSON file")
        print("  queue_name: RabbitMQ queue name (default: sensor_readings)")
        print("  interval: Time between messages in seconds (default: 1.0)")
        sys.exit(1)
    
    limits_file = sys.argv[1]
    hierarchy_file = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2].endswith('.json') else None
    
    # Parse remaining args
    args_start = 3 if hierarchy_file else 2
    queue_name = sys.argv[args_start] if len(sys.argv) > args_start else 'sensor_readings'
    interval = float(sys.argv[args_start+1]) if len(sys.argv) > args_start+1 else 1.0
    
    publish_synthetic_data(limits_file, hierarchy_file, queue_name, interval)