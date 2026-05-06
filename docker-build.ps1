$ErrorActionPreference = "Stop"

Write-Host "Please select an option:"
Write-Host "1) Run using Pre-built Image (Recommended)"
Write-Host "2) Build from Source and Run (For Developers)"
$choice = Read-Host -Prompt "Enter choice [1-2]"

switch ($choice) {
    "1" {
        Write-Host "--- Running with Pre-built Image ---"
        docker compose up -d --remove-orphans --no-build
        Write-Host "Services are starting from remote image."
        Write-Host "Run 'docker compose logs -f' to see the logs."
    }
    "2" {
        Write-Host "--- Building from Source and Running ---"

        $VERSION = (git describe --tags --always --dirty)
        $COMMIT  = (git rev-parse --short HEAD)
        $BUILD_DATE = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

        Write-Host "Building with the following info:"
        Write-Host "  Version: $VERSION"
        Write-Host "  Commit: $COMMIT"
        Write-Host "  Build Date: $BUILD_DATE"
        Write-Host "----------------------------------------"

        $env:CPA_PLUS_IMAGE = "cpa-plus:local"

        Write-Host "Building the Docker image..."
        docker compose build --build-arg VERSION=$VERSION --build-arg COMMIT=$COMMIT --build-arg BUILD_DATE=$BUILD_DATE

        Write-Host "Starting the services..."
        docker compose up -d --remove-orphans --pull never

        Write-Host "Build complete. Services are starting."
        Write-Host "Run 'docker compose logs -f' to see the logs."
    }
    default {
        Write-Host "Invalid choice. Please enter 1 or 2."
        exit 1
    }
}
