.PHONY: build run clean test docker

BINARY_NAME=envoy-prometheus-exporter
CONFIG_FILE=envoy_config.xml

build:
	go build -o $(BINARY_NAME) .

run: build
	./$(BINARY_NAME) $(CONFIG_FILE)

clean:
	go clean
	rm -f $(BINARY_NAME)

test:
	go test -v ./...

# Build for different platforms
build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 .

build-windows:
	GOOS=windows GOARCH=amd64 go build -o $(BINARY_NAME)-windows-amd64.exe .

build-darwin:
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY_NAME)-darwin-amd64 .

# Docker commands (optional)
docker-build:
	docker build -t envoy-prometheus-exporter .

docker-run:
	docker run -p 8080:8080 -v $(PWD)/$(CONFIG_FILE):/app/$(CONFIG_FILE) envoy-prometheus-exporter

# Development helpers
deps:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...