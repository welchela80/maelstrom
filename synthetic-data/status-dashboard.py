# status-dashboard-fixed.py
import streamlit as st
import json
import time
from datetime import datetime
from collections import deque, defaultdict
import plotly.graph_objects as go
import csv

# Page config
st.set_page_config(
    page_title="Machine Status Dashboard",
    page_icon="🏭",
    layout="wide",
    initial_sidebar_state="expanded"
)

# Custom CSS
st.markdown("""
<style>
    .status-card {
        padding: 20px;
        border-radius: 10px;
        margin: 10px;
        text-align: center;
        font-weight: bold;
        font-size: 18px;
        min-height: 150px;
        display: flex;
        flex-direction: column;
        justify-content: center;
        align-items: center;
    }
    .status-good {
        background-color: #4CAF50;
        color: white;
    }
    .status-warning {
        background-color: #FFC107;
        color: black;
    }
    .status-critical {
        background-color: #F44336;
        color: white;
    }
    .status-offline {
        background-color: #9E9E9E;
        color: white;
    }
    .status-uncertain {
        background-color: #2196F3;
        color: white;
    }
</style>
""", unsafe_allow_html=True)

# Initialize session state
if 'machine_history' not in st.session_state:
    st.session_state.machine_history = defaultdict(lambda: {
        'percentages': deque(maxlen=100),
        'timestamps': deque(maxlen=100)
    })

if 'system_map' not in st.session_state:
    st.session_state.system_map = {}

def load_system_mappings(limits_file='files/sensor_operational_range.csv'):
    """Load system to machine mappings from CSV"""
    system_to_machines = defaultdict(set)
    
    try:
        with open(limits_file, 'r', newline='') as f:
            reader = csv.DictReader(f)
            for row in reader:
                system = row.get('system', '').strip()
                sensor_name = row.get('machineName:sensorName', '').strip()
                
                if system and sensor_name and ':' in sensor_name:
                    machine_name = sensor_name.split(':', 1)[0]
                    system_to_machines[system].add(machine_name)
        
        # Convert sets to sorted lists
        result = {system: sorted(list(machines)) for system, machines in system_to_machines.items()}
        st.session_state.system_map = result
        return result
    except FileNotFoundError:
        st.error(f"Could not find {limits_file}. System filtering will not work properly.")
        return {}
    except Exception as e:
        st.error(f"Error loading system mappings: {e}")
        return {}

def get_machine_system(machine_name):
    """Get the system that a machine belongs to"""
    for system, machines in st.session_state.system_map.items():
        if machine_name in machines:
            return system
    return "Unknown"

def group_machines_by_system(machine_names):
    """Group machines by their system"""
    system_groups = defaultdict(list)
    for name in machine_names:
        system = get_machine_system(name)
        system_groups[system].append(name)
    return dict(system_groups)

def load_machine_status():
    """Load machine status from JSON"""
    try:
        with open('machine_status.json', 'r') as f:
            data = json.load(f)
            current_time = datetime.now()
            for machine_name, status in data.items():
                history = st.session_state.machine_history[machine_name]
                history['percentages'].append(status['avg_percentage'])
                history['timestamps'].append(current_time)
            return data
    except:
        return None

def get_status_class(status):
    return {
        'GOOD': 'status-good',
        'WARNING': 'status-warning',
        'CRITICAL': 'status-critical',
        'OFFLINE': 'status-offline',
        'UNCERTAIN': 'status-uncertain'
    }.get(status, 'status-uncertain')

def get_status_emoji(status):
    return {
        'GOOD': '🟢',
        'WARNING': '🟡',
        'CRITICAL': '🔴',
        'OFFLINE': '⚪',
        'UNCERTAIN': '🔵'
    }.get(status, '🔵')

# Sidebar navigation
st.sidebar.title("Navigation")
page = st.sidebar.radio("Select Page", ["📊 Status Cards", "📈 Timelines"])

# Load data
machine_status = load_machine_status()

# Load system mappings on first run
if not st.session_state.system_map:
    load_system_mappings()

if machine_status:
    machines = sorted(machine_status.keys())
    
    # Use system_map for grouping, NOT derived from machine names
    if st.session_state.system_map:
        # Filter to only include machines that exist in current status
        machine_groups = {}
        for system, system_machines in st.session_state.system_map.items():
            existing_machines = [m for m in system_machines if m in machine_status]
            if existing_machines:
                machine_groups[system] = existing_machines
    else:
        # Fallback to prefix-based grouping if CSV didn't load
        machine_groups = group_machines_by_system(machines)
    
    # ==================== PAGE 1: STATUS CARDS ====================
    if page == "📊 Status Cards":
        st.title("🏭 Machine Status Overview")
        
        st.sidebar.header("Filters")
        
        # Debug: Show what systems were loaded
        if st.sidebar.checkbox("Show Debug Info", value=False):
            st.sidebar.write("System Map:", st.session_state.system_map)
            st.sidebar.write("Machine Groups:", machine_groups)
        
        # System filter
        all_systems = sorted(machine_groups.keys())
        system_options = ["All Systems"] + all_systems
        selected_system = st.sidebar.selectbox("System", options=system_options, index=0)
        
        # Get machines for selected system
        if selected_system == "All Systems":
            available_machines = list(machines)
        else:
            # Use machine_groups which is built from system_map
            available_machines = machine_groups.get(selected_system, [])
        
        # Machine filter (within selected system)
        machine_options = ["All Machines"] + sorted(available_machines)
        selected_machine = st.sidebar.selectbox("Machine", options=machine_options, index=0)
        
        # Status filter
        status_options = ["All Status", "GOOD", "WARNING", "CRITICAL", "OFFLINE", "UNCERTAIN"]
        selected_status = st.sidebar.selectbox("Status", options=status_options, index=0)
        
        # Apply filters
        if selected_machine == "All Machines":
            filtered_machines = list(available_machines)
        else:
            filtered_machines = [selected_machine]
        
        # Apply status filter
        if selected_status != "All Status":
            filtered_machines = [m for m in filtered_machines if m in machine_status and machine_status[m]['status'] == selected_status]
        else:
            filtered_machines = [m for m in filtered_machines if m in machine_status]
        
        filtered_machines = sorted(filtered_machines)
        
        # Display cards
        if filtered_machines:
            cols_per_row = 3
            for i in range(0, len(filtered_machines), cols_per_row):
                cols = st.columns(cols_per_row)
                for j, col in enumerate(cols):
                    if i + j < len(filtered_machines):
                        machine_name = filtered_machines[i + j]
                        machine = machine_status[machine_name]
                        with col:
                            status_class = get_status_class(machine['status'])
                            emoji = get_status_emoji(machine['status'])
                            st.markdown(f"""
                            <div class="status-card {status_class}">
                                <div style="font-size: 24px; margin-bottom: 10px;">{emoji} {machine_name}</div>
                                <div style="font-size: 32px; margin: 10px 0;">{machine['avg_percentage']:.1f}%</div>
                                <div style="font-size: 14px; opacity: 0.9;">{machine['running']}</div>
                                <div style="font-size: 14px; opacity: 0.9;">{machine.get('overall_trend', 'No Trend')}</div>
                                <div style="font-size: 12px; margin-top: 10px;">
                                    ✓{machine['good_sensors']} ⚠{machine['warning_sensors']} 
                                    ⚪{machine['offline_sensors']} ✗{machine['fault_sensors']}
                                </div>
                            </div>
                            """, unsafe_allow_html=True)
        else:
            st.info("No machines match the selected filters")
    
    # ==================== PAGE 2: TIMELINES ====================
    elif page == "📈 Timelines":
        st.title("📈 Machine Timelines")
        st.sidebar.header("Timeline Options")
        
        view_mode = st.sidebar.radio("View Mode", ["Individual Machines", "Grouped by System"])
        
        if view_mode == "Individual Machines":
            # System filter first
            all_systems = sorted([s for s in machine_groups.keys() if s != "Unknown"])
            system_options = ["All Systems"] + all_systems
            selected_system = st.sidebar.selectbox("Filter by System", options=system_options, index=0)
            
            # Get machines for selected system
            if selected_system == "All Systems":
                available_machines = machines
            else:
                available_machines = machine_groups.get(selected_system, [])
            
            # Machine selection
            machine_options = ["Select a machine..."] + sorted(available_machines)
            selected_machine = st.sidebar.selectbox("Select Machine", options=machine_options, index=0)
            
            if selected_machine != "Select a machine...":
                history = st.session_state.machine_history[selected_machine]
                if len(history['percentages']) > 0:
                    fig = go.Figure()
                    fig.add_trace(go.Scatter(
                        x=list(history['timestamps']),
                        y=list(history['percentages']),
                        mode='lines+markers',
                        name=selected_machine,
                        line=dict(width=2),
                        marker=dict(size=4)
                    ))
                    fig.add_hline(y=20, line_dash="dash", line_color="blue", annotation_text="20% (Low Threshold)")
                    fig.add_hline(y=80, line_dash="dash", line_color="orange", annotation_text="80% (High Threshold)")
                    fig.add_hrect(y0=20, y1=80, fillcolor="green", opacity=0.1)
                    fig.update_layout(
                        title=f"{selected_machine} - Average Percentage Over Time",
                        xaxis_title="Time",
                        yaxis_title="Average %",
                        yaxis=dict(range=[-10, 110]),
                        height=400,
                        showlegend=False
                    )
                    st.plotly_chart(fig, use_container_width=True)
            else:
                st.info("Please select a machine from the dropdown to view its timeline")
        
        else:  # Grouped by System
            # System selection
            all_systems = sorted([s for s in machine_groups.keys() if s != "Unknown"])
            selected_system = st.sidebar.selectbox("Select System", options=all_systems, index=0 if all_systems else None)
            
            if selected_system:
                group_machines_list = sorted(machine_groups[selected_system])
                
                # Machine selection within system - DEFAULT TO ALL
                all_machines_option = f"All Machines in {selected_system} ({len(group_machines_list)})"
                machine_options = [all_machines_option] + group_machines_list
                selected_option = st.sidebar.selectbox("Select Machine(s)", options=machine_options, index=0)  # Default to index 0 = ALL
                
                # Determine which machines to display
                selected_machines_in_group = group_machines_list if selected_option == all_machines_option else [selected_option]
                
                if selected_machines_in_group:
                    # Create combined timeline
                    fig = go.Figure()
                    for machine_name in selected_machines_in_group:
                        history = st.session_state.machine_history[machine_name]
                        if len(history['percentages']) > 0:
                            status = machine_status[machine_name]['status']
                            color_map = {'GOOD': 'green', 'WARNING': 'orange', 'CRITICAL': 'red', 'OFFLINE': 'gray', 'UNCERTAIN': 'blue'}
                            color = color_map.get(status, 'blue')
                            fig.add_trace(go.Scatter(
                                x=list(history['timestamps']),
                                y=list(history['percentages']),
                                mode='lines+markers',
                                name=machine_name,
                                line=dict(width=2, color=color),
                                marker=dict(size=4)
                            ))
                    
                    fig.add_hline(y=20, line_dash="dash", line_color="blue", annotation_text="20% (Low Threshold)")
                    fig.add_hline(y=80, line_dash="dash", line_color="orange", annotation_text="80% (High Threshold)")
                    fig.add_hrect(y0=20, y1=80, fillcolor="green", opacity=0.1)
                    fig.update_layout(
                        title=f"{selected_system} System - Average Percentage Over Time",
                        xaxis_title="Time",
                        yaxis_title="Average %",
                        yaxis=dict(range=[-10, 110]),
                        height=600,
                        showlegend=True,
                        legend=dict(yanchor="top", y=0.99, xanchor="left", x=0.01)
                    )
                    st.plotly_chart(fig, use_container_width=True)
                    
                    # Stats table
                    st.subheader(f"Detailed Statistics")
                    stats_data = []
                    for machine_name in selected_machines_in_group:
                        machine = machine_status[machine_name]
                        stats_data.append({
                            'Machine': machine_name,
                            'Status': f"{get_status_emoji(machine['status'])} {machine['status']}",
                            'Avg %': f"{machine['avg_percentage']:.1f}%",
                            'Running': machine.get('overall_trend', 'No Trend'),
                            'Trend': machine['overall_trend'],
                            'Good': machine['good_sensors'],
                            'Warning': machine['warning_sensors'],
                            'Offline': machine['offline_sensors'],
                            'Fault': machine['fault_sensors']
                        })
                    st.dataframe(stats_data, use_container_width=True, hide_index=True)

else:
    st.warning("⚠️ Waiting for data from Go consumer...")
    st.info("Make sure the Go consumer is running and generating machine_status.json")

# Auto-refresh - moved to absolute end
time.sleep(2)
st.rerun()