import csv
import json
import sys

# Check if enough arguments were provided
if len(sys.argv) < 2:
    print("Error: Please provide input JSON file as argument")
    sys.exit(1)

input_json = sys.argv[1]

# Load JSON data
with open(input_json, 'r') as f:
    data = json.load(f)

# Prepare output data
output_rows = []

print("=== Processing Data ===")

# Iterate through ships
for ship_idx, ship in enumerate(data.get('ships', [])):
    print(f"\nShip {ship_idx + 1}: {ship.get('ship_name', 'Unknown')}")
    
    # mappings is a list
    mappings = ship.get('mappings', [])
    print(f"  Type of mappings: {type(mappings)}")
    print(f"  Number of mappings: {len(mappings)}")
    
    # Iterate through mappings list
    for mapping_dict in mappings:
        # Each item in the list is a dict with the machine name as the key
        for machine_key, mapping in mapping_dict.items():
            machine_name = mapping.get('machine_name', machine_key)
            sensors = mapping.get('sensors', {})
            
            print(f"  Machine: {machine_name}")
            print(f"    Number of sensors: {len(sensors)}")
            
            # Iterate through each sensor
            for sensor_name, sensor_data in sensors.items():
                operational_high = sensor_data.get('OperationalHigh', '')
                operational_low = sensor_data.get('OperationalLow', '')
                
                # Create the combined name
                combined_name = f"{machine_name}:{sensor_name}"
                
                output_rows.append({
                    'machineName:sensorName': combined_name,
                    'operationalHigh': operational_high,
                    'operationalLow': operational_low
                })

print(f"\n=== Summary ===")
print(f"Total rows collected: {len(output_rows)}")

if len(output_rows) > 0:
    print(f"First few rows: {output_rows[:3]}")

# Write to CSV
output_csv = './files/sensor_operational_range.csv'
with open(output_csv, 'w', newline='') as f:
    fieldnames = ['machineName:sensorName', 'operationalHigh', 'operationalLow']
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    
    writer.writeheader()
    writer.writerows(output_rows)

print(f"\nSuccessfully wrote {len(output_rows)} rows to {output_csv}")