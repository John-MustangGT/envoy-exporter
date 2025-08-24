### **Key Features:**

1. **XML Configuration**: Reads user credentials, Envoy details, and query URLs from an XML file
2. **Token Management**: Automatically handles Enphase authentication and token renewal
3. **Prometheus Metrics**: Serves metrics at `/metrics` endpoint in Prometheus format
4. **Multiple Endpoints**: Supports all the query types from the original shell script
5. **Health Monitoring**: Provides `/health` endpoint for monitoring

### **Program Structure:**

- **Authentication**: Automatically logs in to Enphase and gets web tokens
- **Token Refresh**: Background goroutine refreshes tokens before expiry
- **HTTP Server**: Serves on configurable port with multiple endpoints
- **Metric Parsing**: Converts Envoy JSON responses to Prometheus format
- **Error Handling**: Comprehensive error handling and logging

### **Supported Metrics:**

- Production data (watts now, daily/lifetime watt-hours)
- Individual inverter performance
- Meter status and readings
- Live data (PV, grid, load power)
- Connection status
- Token expiry information

### **Usage:**

1. **Edit the config file** with your credentials and Envoy details
2. **Build**: `make build` or `go build`
3. **Run**: `./envoy-prometheus-exporter envoy_config.xml`
4. **Access**: 
   - Metrics: `http://localhost:8080/metrics`
   - Health: `http://localhost:8080/health`

### **Configuration Example:**

The XML config supports the `{envoy_ip}` placeholder which gets replaced with your actual Envoy IP address in the queries.

The program automatically handles the same authentication flow and maintains the token in memory and refreshes it as needed, making it suitable for continuous operation with Prometheus scraping.

### **Referances** 
[Prometheus]: https://prometheus.io/docs/prometheus/latest/getting_started/
[Enphase]: https://enphase.com/download/iq-gateway-local-apis-or-ui-access-using-token
