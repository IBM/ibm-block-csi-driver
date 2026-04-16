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

# Run static code analysis
EXIT_STATUS=0
${CONTAINER_CMD} run --entrypoint ./controllers/scripts/pycodestyle.sh --rm csi-controller-tests || EXIT_STATUS=$?
${CONTAINER_CMD} run --entrypoint ./controllers/scripts/pylint.sh --rm csi-controller-tests || EXIT_STATUS=$?
exit ${EXIT_STATUS}
