#!/bin/bash
set -xe
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

# Add verbosity and explicit test discovery to ensure tests are found
echo "Starting unit tests with pytest..."
echo "Coverage directory: $coveragedir"
echo "Python path: $PYTHONPATH"
echo "Working directory: $(pwd)"
echo "Architecture: $(uname -m)"
echo "Python version: $(python3 --version)"

# Check for dependencies on s390x
if [[ "$(uname -m)" == "s390x" ]]; then
    echo "Running on s390x (zLinux) - checking dependencies..."
    python3 -c "import grpc; print('grpcio version:', grpc.__version__)" || echo "Warning: grpcio import failed"
    python3 -c "import sys; print('Testing basic imports...'); import common; import controllers; print('Basic imports OK')" || echo "Warning: basic imports failed"
fi

# Use pytest instead of nose (nose is unmaintained and has issues on s390x)
# pytest is more stable and better maintained
echo "=== Running tests with pytest ==="
pytest \
    --verbose \
    --capture=no \
    --tb=short \
    --cov=common \
    --cov=controllers \
    --cov-report=xml:$coveragedir/.coverage.xml \
    --junit-xml=$coveragedir/.unitests.xml \
    controllers/tests/ \
    $@

echo "Unit tests completed successfully"
