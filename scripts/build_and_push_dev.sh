#!/usr/bin/env bash
# Build the controller image from local sources and push to quay.io.
#
# Usage:
#   ./scripts/build_and_push_dev.sh <tag>
#
# Example:
#   ./scripts/build_and_push_dev.sh sdk9
#
# The script:
#   1. Builds the ibm_svc_rest_client wheel from the sibling
#      IBMStorageVirtualizeRestAPI repo.
#   2. Copies the wheel into build/wheels/ (replacing any previous version).
#   3. Builds the controller Docker image (Dockerfile-csi-controller).
#   4. Tags and pushes to quay.io/csiblock/ibm-block-csi-driver-controller-amd64:<tag>
#
# Prerequisites: docker, python3 (with build module), access to quay.io/csiblock

set -euo pipefail

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
    echo "Usage: $0 <tag>"
    exit 1
fi

REGISTRY="quay.io/csiblock/ibm-block-csi-driver-controller-amd64"
FULL_IMAGE="${REGISTRY}:${TAG}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRIVER_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SDK_REPO="$(cd "${DRIVER_ROOT}/../IBMStorageVirtualizeRestAPI" && pwd)"
WHEELS_DIR="${DRIVER_ROOT}/build/wheels"

echo "=== Step 1: Build SDK wheel from ${SDK_REPO} ==="
cd "${SDK_REPO}"
python3 -m build --wheel --outdir /tmp/svc_sdk_wheel 2>&1 | tail -5
WHEEL_PATH="$(ls -t /tmp/svc_sdk_wheel/ibm_svc_rest_client-*.whl | head -1)"
echo "Built: ${WHEEL_PATH}"

echo "=== Step 2: Copy wheel to ${WHEELS_DIR} ==="
mkdir -p "${WHEELS_DIR}"
rm -f "${WHEELS_DIR}"/ibm_svc_rest_client-*.whl
cp "${WHEEL_PATH}" "${WHEELS_DIR}/"
echo "Copied: $(ls "${WHEELS_DIR}"/ibm_svc_rest_client-*.whl)"

echo "=== Step 3: Build controller image ==="
cd "${DRIVER_ROOT}"
docker build \
    -t "${FULL_IMAGE}" \
    -f Dockerfile-csi-controller \
    --platform linux/amd64 \
    --build-arg VERSION="1.14.0" \
    --build-arg BUILD_NUMBER="0" \
    .

echo "=== Step 4: Push ${FULL_IMAGE} ==="
docker push "${FULL_IMAGE}"

echo ""
echo "Done. Image pushed: ${FULL_IMAGE}"
