# publisher.py
import pika
import csv
import json
import time
import sys
import os
from datetime import datetime
from pathlib import Path

def find_csv_files(directory):
    """
    Recursively find all CSV files in directory
    """
    csv_files = []
    for root, dirs, files in os.walk(directory):
        for file in files:
            if file.endswith('.csv'):
                csv_files.append(os.path.join(root, file))
    return sorted(csv_files)

def publish_directory_round_robin(directory, queue_name='sensor_readings', interval=1.0, loop=False):
    """
    Reads CSV files from a directory in round-robin fashion:
    - Read one row from file 1
    - Read one row from file 2
    - Read one row from file 3
    - ... repeat until all files are exhausted
    
    Args:
        directory: Path to directory containing CSV files
        queue_name: RabbitMQ queue name
        interval: Time between messages in seconds (default 1.0 for 1Hz)
        loop: If True, continuously loop through all files
    """
    
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
    
    # Find all CSV files
    csv_files = find_csv_files(directory)
    
    if not csv_files:
        print(f"No CSV files found in {directory}")
        connection.close()
        return
    
    print(f"Found {len(csv_files)} CSV files:")
    for f in csv_files:
        print(f"  - {f}")
    print(f"\nStarting round-robin publishing at {1/interval}Hz")
    
    total_published = 0
    
    try:
        while True:  # Main loop for continuous operation if loop=True
            # Open all files and create readers
            file_handles = []
            readers = []
            
            for csv_file in csv_files:
                try:
                    fh = open(csv_file, 'r')
                    reader = csv.DictReader(fh)
                    file_handles.append(fh)
                    readers.append({
                        'reader': reader,
                        'file': csv_file,
                        'active': True
                    })
                except Exception as e:
                    print(f"Error opening {csv_file}: {e}")
            
            if not readers:
                print("No files could be opened")
                break
            
            # Read round-robin until all files are exhausted
            active_files = len(readers)
            
            while active_files > 0:
                for reader_info in readers:
                    if not reader_info['active']:
                        continue
                    
                    try:
                        row = next(reader_info['reader'])
                        
                        # Create message with timestamp and all sensor readings
                        message = {
                            'timestamp': datetime.utcnow().isoformat(),
                            'source_file': os.path.basename(reader_info['file']),
                            'readings': row
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
                        
                        total_published += 1
                        if total_published % 100 == 0:
                            print(f"Published {total_published} total messages...")
                        
                        # Wait for interval
                        time.sleep(interval)
                        
                    except StopIteration:
                        # This file is exhausted
                        reader_info['active'] = False
                        active_files -= 1
                        print(f"  ✓ Completed: {os.path.basename(reader_info['file'])}")
            
            # Close all file handles
            for fh in file_handles:
                fh.close()
            
            print(f"\nCompleted one pass through all files. Total published: {total_published}")
            
            if not loop:
                break
            else:
                print("Looping back to start of files...\n")
                
    except KeyboardInterrupt:
        print("\n\nStopping publisher...")
    finally:
        connection.close()
        print(f"Published {total_published} total messages. Connection closed.")

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: python publisher.py <directory> [queue_name] [interval] [--loop]")
        print("  directory: Path to directory containing CSV files")
        print("  queue_name: RabbitMQ queue name (default: sensor_readings)")
        print("  interval: Time between messages in seconds (default: 1.0)")
        print("  --loop: Continuously loop through files")
        sys.exit(1)
    
    directory = sys.argv[1]
    queue_name = sys.argv[2] if len(sys.argv) > 2 and not sys.argv[2].startswith('--') else 'sensor_readings'
    interval = float(sys.argv[3]) if len(sys.argv) > 3 and not sys.argv[3].startswith('--') else 1.0
    loop = '--loop' in sys.argv
    
    if not os.path.isdir(directory):
        print(f"Error: {directory} is not a directory")
        sys.exit(1)
    
    publish_directory_round_robin(directory, queue_name, interval, loop)