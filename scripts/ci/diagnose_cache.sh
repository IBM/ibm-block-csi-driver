#!/bin/bash
# Diagnostic script to check Docker cache status on zLinux

set -e

echo "=== Docker Cache Diagnostics ==="
echo ""

# Detect container runtime
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
else
    echo "Error: Neither podman nor docker found"
    exit 1
fi

echo "Container runtime: ${CONTAINER_CMD}"
echo "Architecture: $(uname -m)"
echo ""

# Check Docker volumes
echo "=== Docker Volumes ==="
ARCH=$(uname -m)
VOLUME_NAME="pip-cache-${ARCH}"
echo "Looking for volume: ${VOLUME_NAME}"

if ${CONTAINER_CMD} volume inspect ${VOLUME_NAME} &> /dev/null; then
    echo "✓ Volume exists"
    ${CONTAINER_CMD} volume inspect ${VOLUME_NAME}
    echo ""
    
    # Check volume contents
    echo "=== Volume Contents ==="
    if [ "${CONTAINER_CMD}" = "podman" ]; then
        VOLUME_PATH=$(${CONTAINER_CMD} volume inspect ${VOLUME_NAME} --format '{{.Mountpoint}}')
        echo "Volume path: ${VOLUME_PATH}"
        if [ -d "${VOLUME_PATH}" ]; then
            echo "Contents:"
            sudo ls -lah "${VOLUME_PATH}" 2>/dev/null || echo "Cannot list (permission denied)"
            echo ""
            echo "Checking for grpcio cache:"
            sudo find "${VOLUME_PATH}" -name "*grpcio*" 2>/dev/null | head -20 || echo "No grpcio cache found"
        fi
    else
        echo "To inspect Docker volume contents, run:"
        echo "  docker run --rm -v ${VOLUME_NAME}:/cache alpine ls -lah /cache"
    fi
else
    echo "✗ Volume does NOT exist"
fi

echo ""
echo "=== BuildKit Cache ==="
if [ "${CONTAINER_CMD}" = "docker" ]; then
    echo "Checking BuildKit cache..."
    docker buildx du 2>/dev/null || echo "BuildKit cache info not available (buildx not installed or no cache)"
else
    echo "Podman doesn't have a direct BuildKit cache inspection command"
fi

echo ""
echo "=== Recent Images ==="
${CONTAINER_CMD} images | grep -E "(csi-controller|csi-host-definer|python-39)" | head -10

echo ""
echo "=== BuildKit Status ==="
if [ "${CONTAINER_CMD}" = "docker" ]; then
    echo "DOCKER_BUILDKIT env: ${DOCKER_BUILDKIT:-not set}"
    docker version --format '{{.Server.Version}}' 2>/dev/null || echo "Cannot get Docker version"
fi

echo ""
echo "=== Recommendations ==="
echo "1. Verify volume is mounted at /opt/app-root/.cache/pip (not /root/.cache/pip)"
echo "2. Check that builds run as uid 1001 (default user)"
echo "3. Ensure DOCKER_BUILDKIT=1 is set for production builds"
echo "4. Check if cache is being invalidated by file changes"

# Made with Bob
