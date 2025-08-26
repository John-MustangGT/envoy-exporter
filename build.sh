#!/bin/bash
# build.sh - Comprehensive build script with version information

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Binary name
BINARY_NAME="envoy-prometheus-exporter"

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Get build information
get_build_info() {
    BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    BUILD_USER=$(whoami)
    BUILD_HOST=$(hostname)
    GO_VERSION=$(go version)
    
    # Git information
    if git rev-parse --git-dir > /dev/null 2>&1; then
        GIT_COMMIT=$(git rev-parse --short HEAD)
        GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
        GIT_TAG=$(git describe --tags --exact-match 2>/dev/null || echo "")
        
        # Check if working directory is dirty
        if ! git diff --quiet 2>/dev/null; then
            GIT_DIRTY="-dirty"
        else
            GIT_DIRTY=""
        fi
        
        # Determine version
        if [ -n "$GIT_TAG" ]; then
            VERSION="$GIT_TAG"
        else
            VERSION="v0.0.0-$GIT_COMMIT$GIT_DIRTY"
        fi
    else
        log_warning "Not in a git repository, using default version info"
        GIT_COMMIT="unknown"
        GIT_BRANCH="unknown"
        GIT_TAG=""
        GIT_DIRTY=""
        VERSION="v0.0.0-unknown"
    fi
    
    # Platform information
    GOOS=$(go env GOOS)
    GOARCH=$(go env GOARCH)
    PLATFORM="$GOOS/$GOARCH"
    
    # Go module information
    if [ -f "go.mod" ]; then
        MODULE_NAME=$(go list -m 2>/dev/null || echo "envoy-prometheus-exporter")
        GO_MOD_VERSION=$(go list -m -f '{{.GoVersion}}' 2>/dev/null || echo "unknown")
    else
        MODULE_NAME="envoy-prometheus-exporter"
        GO_MOD_VERSION="unknown"
    fi
}

# Build ldflags
build_ldflags() {
    LDFLAGS="-s -w"
    LDFLAGS="$LDFLAGS -X 'main.Version=$VERSION'"
    LDFLAGS="$LDFLAGS -X 'main.GitCommit=$GIT_COMMIT'"
    LDFLAGS="$LDFLAGS -X 'main.GitBranch=$GIT_BRANCH'"
    LDFLAGS="$LDFLAGS -X 'main.BuildTime=$BUILD_TIME'"
    LDFLAGS="$LDFLAGS -X 'main.BuildUser=$BUILD_USER'"
    LDFLAGS="$LDFLAGS -X 'main.BuildHost=$BUILD_HOST'"
    LDFLAGS="$LDFLAGS -X 'main.GoVersion=$GO_VERSION'"
    LDFLAGS="$LDFLAGS -X 'main.Platform=$PLATFORM'"
    LDFLAGS="$LDFLAGS -X 'main.ModuleName=$MODULE_NAME'"
    LDFLAGS="$LDFLAGS -X 'main.GoModVersion=$GO_MOD_VERSION'"
}

# Generate web version file
generate_web_version() {
    log_info "Generating web version information..."
    
    mkdir -p web
    cat > web/version.json << EOF
{
    "version": "$VERSION",
    "git_commit": "$GIT_COMMIT",
    "git_branch": "$GIT_BRANCH",
    "build_time": "$BUILD_TIME",
    "build_user": "$BUILD_USER",
    "build_host": "$BUILD_HOST",
    "go_version": "$GO_VERSION",
    "platform": "$PLATFORM",
    "module_name": "$MODULE_NAME"
}
EOF
    log_success "Web version file created: web/version.json"
}

# Print build information
print_build_info() {
    log_info "Build Information:"
    echo "  Version:      $VERSION"
    echo "  Git Commit:   $GIT_COMMIT"
    echo "  Git Branch:   $GIT_BRANCH"
    echo "  Build Time:   $BUILD_TIME"
    echo "  Build User:   $BUILD_USER@$BUILD_HOST"
    echo "  Go Version:   $GO_VERSION"
    echo "  Platform:     $PLATFORM"
    echo "  Module:       $MODULE_NAME"
    echo "  Go Mod Ver:   $GO_MOD_VERSION"
    echo ""
}

# Build single platform
build_single() {
    local target_os="$1"
    local target_arch="$2"
    local output_name="$3"
    
    log_info "Building for $target_os/$target_arch..."
    
    GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
    go build -ldflags "$LDFLAGS" -o "$output_name" .
    
    if [ $? -eq 0 ]; then
        log_success "Built: $output_name"
        if command -v file >/dev/null 2>&1; then
            file "$output_name"
        fi
        ls -lh "$output_name"
    else
        log_error "Failed to build for $target_os/$target_arch"
        return 1
    fi
}

# Build for current platform
build_current() {
    log_info "Building $BINARY_NAME for current platform ($PLATFORM)..."
    
    # Install dependencies
    log_info "Installing dependencies..."
    go mod download
    go mod tidy
    
    # Build
    CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$BINARY_NAME" .
    
    if [ $? -eq 0 ]; then
        log_success "Built: $BINARY_NAME"
        ./"$BINARY_NAME" --version
    else
        log_error "Build failed"
        exit 1
    fi
}

# Build for multiple platforms
build_all() {
    log_info "Building for multiple platforms..."
    
    # Create dist directory
    mkdir -p dist
    
    # Install dependencies
    log_info "Installing dependencies..."
    go mod download
    go mod tidy
    
    # Define build targets
    declare -a targets=(
        "linux:amd64:$BINARY_NAME-linux-amd64"
        "linux:arm64:$BINARY_NAME-linux-arm64"
        "linux:arm:$BINARY_NAME-linux-arm"
        "darwin:amd64:$BINARY_NAME-darwin-amd64"
        "darwin:arm64:$BINARY_NAME-darwin-arm64"
        "windows:amd64:$BINARY_NAME-windows-amd64.exe"
        "freebsd:amd64:$BINARY_NAME-freebsd-amd64"
        "openbsd:amd64:$BINARY_NAME-openbsd-amd64"
    )
    
    # Build each target
    for target in "${targets[@]}"; do
        IFS=':' read -r os arch output <<< "$target"
        build_single "$os" "$arch" "dist/$output"
    done
    
    log_success "All builds completed"
    echo ""
    log_info "Build artifacts:"
    ls -la dist/
}

# Build Docker image
build_docker() {
    local image_tag="${1:-$BINARY_NAME:$VERSION}"
    
    log_info "Building Docker image: $image_tag"
    
    docker build \
        --build-arg VERSION="$VERSION" \
        --build-arg GIT_COMMIT="$GIT_COMMIT" \
        --build-arg GIT_BRANCH="$GIT_BRANCH" \
        --build-arg BUILD_TIME="$BUILD_TIME" \
        --build-arg BUILD_USER="$BUILD_USER" \
        --build-arg BUILD_HOST="$BUILD_HOST" \
        -t "$image_tag" \
        .
    
    if [ $? -eq 0 ]; then
        log_success "Docker image built: $image_tag"
        docker images "$image_tag"
    else
        log_error "Docker build failed"
        return 1
    fi
}

# Create release packages
create_packages() {
    log_info "Creating release packages..."
    
    if [ ! -d "dist" ]; then
        log_error "No dist directory found. Run 'build-all' first."
        return 1
    fi
    
    mkdir -p release
    
    # Create source package
    if git rev-parse --git-dir > /dev/null 2>&1; then
        git archive --format=tar.gz --prefix="$BINARY_NAME-$VERSION/" HEAD > "release/$BINARY_NAME-$VERSION-source.tar.gz"
        log_success "Source package: release/$BINARY_NAME-$VERSION-source.tar.gz"
    fi
    
    # Package binaries
    cd dist
    for binary in *; do
        if [ -f "$binary" ]; then
            tar -czf "../release/$binary-$VERSION.tar.gz" "$binary"
            log_success "Binary package: release/$binary-$VERSION.tar.gz"
        fi
    done
    cd ..
    
    log_success "Release packages created in release/ directory"
    ls -la release/
}

# Run tests
run_tests() {
    log_info "Running tests..."
    
    if [ ! -f "go.mod" ]; then
        log_error "No go.mod file found"
        return 1
    fi
    
    go test -v -race -coverprofile=coverage.out ./...
    
    if [ $? -eq 0 ]; then
        log_success "All tests passed"
        
        # Generate coverage report if go tool cover is available
        if command -v go >/dev/null 2>&1; then
            go tool cover -html=coverage.out -o coverage.html
            log_success "Coverage report: coverage.html"
        fi
    else
        log_error "Tests failed"
        return 1
    fi
}

# Clean build artifacts
clean() {
    log_info "Cleaning build artifacts..."
    
    rm -f "$BINARY_NAME" "${BINARY_NAME}-dev"
    rm -rf dist/ release/
    rm -f coverage.out coverage.html
    rm -f web/version.json
    
    log_success "Build artifacts cleaned"
}

# Install binary
install_binary() {
    if [ ! -f "$BINARY_NAME" ]; then
        log_error "Binary not found. Run 'build' first."
        return 1
    fi
    
    log_info "Installing $BINARY_NAME to /usr/local/bin..."
    
    if [ "$EUID" -ne 0 ]; then
        sudo cp "$BINARY_NAME" /usr/local/bin/
    else
        cp "$BINARY_NAME" /usr/local/bin/
    fi
    
    if [ $? -eq 0 ]; then
        log_success "$BINARY_NAME installed to /usr/local/bin/"
        /usr/local/bin/"$BINARY_NAME" --version
    else
        log_error "Installation failed"
        return 1
    fi
}

# Show help
show_help() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  build       Build for current platform"
    echo "  build-all   Build for multiple platforms"
    echo "  docker      Build Docker image"
    echo "  test        Run tests"
    echo "  clean       Clean build artifacts"
    echo "  package     Create release packages"
    echo "  install     Install binary to /usr/local/bin"
    echo "  info        Show build information"
    echo "  version     Generate web version file"
    echo "  help        Show this help"
    echo ""
    echo "Environment variables:"
    echo "  VERSION     Override version (default: auto-detected)"
    echo "  DOCKER_TAG  Docker image tag (default: $BINARY_NAME:\$VERSION)"
    echo ""
}

# Main script
main() {
    # Get build information
    get_build_info
    build_ldflags
    
    # Parse command
    case "${1:-build}" in
        "build")
            print_build_info
            generate_web_version
            build_current
            ;;
        "build-all")
            print_build_info
            generate_web_version
            build_all
            ;;
        "docker")
            print_build_info
            generate_web_version
            build_docker "${DOCKER_TAG:-$BINARY_NAME:$VERSION}"
            ;;
        "test")
            run_tests
            ;;
        "clean")
            clean
            ;;
        "package")
            create_packages
            ;;
        "install")
            install_binary
            ;;
        "info")
            print_build_info
            ;;
        "version")
            generate_web_version
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            log_error "Unknown command: $1"
            echo ""
            show_help
            exit 1
            ;;
    esac
}

# Check prerequisites
check_prerequisites() {
    # Check if Go is installed
    if ! command -v go >/dev/null 2>&1; then
        log_error "Go is not installed or not in PATH"
        exit 1
    fi
    
    # Check Go version
    go_version=$(go version | grep -o 'go[0-9]\+\.[0-9]\+' | sed 's/go//')
    required_version="1.19"
    
    if [ "$(printf '%s\n' "$required_version" "$go_version" | sort -V | head -n1)" != "$required_version" ]; then
        log_warning "Go version $go_version detected, recommend $required_version or higher"
    fi
}

# Run main function
check_prerequisites
main "$@"
