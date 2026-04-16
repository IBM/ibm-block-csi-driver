#!/bin/bash
# Build script for csi-controller-tests image with persistent pip cache
# This script creates and uses architecture-specific pip cache volumes to speed up builds,
# especially on non-x86 platforms (s390x, ppc64le) where grpcio must be compiled from source.

set -e

ARCH=$(uname -m)
VOLUME_NAME="pip-cache-${ARCH}"
IMAGE_NAME="csi-controller-tests"
DOCKERFILE="Dockerfile-controllers.test"

# Detect container runtime (podman or docker)
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
else
    echo "Error: Neither podman nor docker found"
    exit 1
fi

echo "Using container runtime: ${CONTAINER_CMD}"
echo "Architecture: ${ARCH}"
echo "Cache volume: ${VOLUME_NAME}"

# Create persistent volume if it doesn't exist
if [ "${CONTAINER_CMD}" = "podman" ]; then
    ${CONTAINER_CMD} volume create ${VOLUME_NAME} 2>/dev/null || true
else
    # For Docker, check if volume exists before creating
    if ! ${CONTAINER_CMD} volume inspect ${VOLUME_NAME} &> /dev/null; then
        ${CONTAINER_CMD} volume create ${VOLUME_NAME}
    fi
fi

# Enable BuildKit for Docker (Podman uses it by default)
if [ "${CONTAINER_CMD}" = "docker" ]; then
    export DOCKER_BUILDKIT=1
fi

# Build with persistent pip cache volume
echo "Building ${IMAGE_NAME} with persistent pip cache..."
${CONTAINER_CMD} build \
    --volume ${VOLUME_NAME}:/root/.cache/pip:Z \
    -f ${DOCKERFILE} \
    -t ${IMAGE_NAME} \
    .

echo "Build completed successfully!"
echo "Pip cache persisted in volume: ${VOLUME_NAME}"

# Made with Bob
