# MQTT Publishing - Topic Structure and Payloads

The Envoy Prometheus Exporter can publish key solar system metrics to an MQTT broker. This feature is disabled by default and must be explicitly enabled in the configuration.

## Configuration

Add the following MQTT configuration section to your `envoy_config.xml`:

```xml
<mqtt enabled="true">
    <broker>192.168.1.50</broker>          <!-- MQTT broker hostname/IP -->
    <port>1883</port>                      <!-- MQTT broker port (1883 for TCP, 8883 for TLS) -->
    <username>mqtt_user</username>          <!-- Optional: MQTT username -->
    <password>mqtt_password</password>      <!-- Optional: MQTT password -->
    <client_id>envoy-exporter</client_id>   <!-- Optional: MQTT client ID -->
    <topic_prefix>solar/envoy</topic_prefix><!-- Topic prefix for all messages -->
    <qos>1</qos>                           <!-- QoS level (0, 1, or 2) -->
    <retain>true</retain>                  <!-- Retain messages -->
    <tls>false</tls>                       <!-- Use TLS/SSL -->
    <insecure_tls>false</insecure_tls>     <!-- Skip TLS certificate validation -->
    <publish_interval>60</publish_interval> <!-- Publish interval in seconds -->
</mqtt>
```

## Published Topics

All topics are prefixed with the configured `topic_prefix` (default: `solar/envoy`).

### System Status Topics

| Topic | Type | Description | Example Value |
|-------|------|-------------|---------------|
| `status` | string | Exporter status | `online`, `offline` |
| `system_status` | string | Solar system status | `producing`, `daylight`, `night`, `offline` |
| `power_flow` | string | Power flow direction | `importing`, `exporting`, `idle` |

### Power Metrics

| Topic | Type | Unit | Description |
|-------|------|------|-------------|
| `current_watts` | float | W | Current power production |
| `grid_watts` | float | W | Grid power (+ = import, - = export) |
| `load_watts` | float | W | Current load consumption |

### Energy Metrics

| Topic | Type | Unit | Description |
|-------|------|------|-------------|
| `today_wh` | float | Wh | Energy produced today |
| `lifetime_wh` | float | Wh | Total lifetime energy production |

### System Health

| Topic | Type | Unit | Description |
|-------|------|------|-------------|
| `inverters_online` | integer | count | Number of active inverters |
| `inverters_total` | integer | count | Total number of inverters |
| `system_efficiency` | float | % | System efficiency percentage |
| `self_consumption` | float | % | Self-consumption percentage |
| `solar_coverage` | float | % | Solar coverage of load |

### Complete JSON Payload

| Topic | Type | Description |
|-------|------|-------------|
| `metrics` | JSON | Complete metrics object with all data |

## JSON Payload Structure

The `metrics` topic contains a complete JSON object with all current metrics:

```json
{
  "timestamp": 1693238400,
  "current_watts": 4250.5,
  "today_wh": 28750.0,
  "lifetime_wh": 45678901.2,
  "inverters_online": 24,
  "inverters_total": 24,
  "grid_watts": -1250.3,
  "load_watts": 3000.2,
  "system_efficiency": 100.0,
  "self_consumption": 70.6,
  "solar_coverage": 141.7
}
```

## Publishing Behavior

- **Startup**: Publishes immediately on successful connection
- **Regular Updates**: Publishes every `publish_interval` seconds (default: 60)
- **Connection Status**: Uses MQTT Last Will Testament for clean offline detection
- **Retained Messages**: When `retain=true`, latest values are stored by broker
- **Auto-Reconnect**: Automatically reconnects if connection is lost

## Home Assistant Integration

The MQTT topics are compatible with Home Assistant's MQTT discovery. Example sensor configuration:

```yaml
sensor:
  - platform: mqtt
    name: "Solar Power"
    state_topic: "solar/envoy/current_watts"
    unit_of_measurement: "W"
    device_class: "power"
    
  - platform: mqtt
    name: "Solar Energy Today"
    state_topic: "solar/envoy/today_wh"
    unit_of_measurement: "Wh"
    device_class: "energy"
    
  - platform: mqtt
    name: "Inverters Online"
    state_topic: "solar/envoy/inverters_online"
    
  - platform: mqtt
    name: "System Status"
    state_topic: "solar/envoy/system_status"
```

## Monitoring and Debugging

### API Endpoint
Check MQTT status via HTTP API:
```
GET /api/mqtt-status
```

Returns:
```json
{
  "enabled": true,
  "connected": true,
  "broker": "192.168.1.50:1883",
  "topic_prefix": "solar/envoy",
  "publish_interval": 60,
  "last_publish": 1693238400
}
```

### Log Messages
The exporter logs MQTT activity:
```
2024-08-25 16:20:00 MQTT: Connected to broker tcp://192.168.1.50:1883
2024-08-25 16:21:00 MQTT: Published metrics - Power: 4250.5W, Inverters: 24/24, Grid: -1250.3W
```

### Manual Testing
Subscribe to all topics with a command-line client:
```bash
mosquitto_sub -h 192.168.1.50 -t "solar/envoy/#" -v
```

## Security Considerations

1. **Authentication**: Use username/password authentication
2. **TLS/SSL**: Enable TLS for encrypted communication
3. **Topic ACLs**: Restrict publishing permissions to your client ID
4. **Network Isolation**: Place MQTT broker on isolated network segment

## Troubleshooting

### Common Issues

1. **Connection Refused**
   - Check broker hostname/IP and port
   - Verify broker is running and accessible
   - Check firewall rules

2. **Authentication Failed**
   - Verify username/password are correct
   - Check broker user permissions

3. **No Messages Published**
   - Check `publish_interval` setting
   - Verify Envoy data is being collected
   - Check MQTT broker logs

4. **TLS Connection Issues**
   - Verify TLS port (usually 8883)
   - Check certificate validity
   - Try `insecure_tls=true` for testing

### Enable Debug Logging

Set log level to debug for more detailed MQTT logging:
```bash
./envoy-exporter -debug
```
