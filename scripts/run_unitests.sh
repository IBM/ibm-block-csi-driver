#!/bin/bash
set -x

# Detect container runtime (podman or docker)
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
else
    echo "Error: Neither podman nor docker found"
    exit 1
fi

# Build using the new script with persistent pip cache
./scripts/build_controllers_test.sh || exit 1

# Run unit tests
[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"
${CONTAINER_CMD} run --entrypoint ./controllers/scripts/unitests.sh --rm -t $coverage csi-controller-tests
