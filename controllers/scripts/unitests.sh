#!/bin/bash
set -xe
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

# Add verbosity and explicit test discovery to ensure tests are found
echo "Starting unit tests with coverage..."
echo "Coverage directory: $coveragedir"
echo "Python path: $PYTHONPATH"
echo "Working directory: $(pwd)"
echo "Architecture: $(uname -m)"
echo "Python version: $(python3 --version)"

# Check for grpcio issues on s390x
if [[ "$(uname -m)" == "s390x" ]]; then
    echo "Running on s390x (zLinux) - checking grpcio installation..."
    python3 -c "import grpc; print('grpcio version:', grpc.__version__)" || echo "Warning: grpcio import failed"
fi

# Run nosetests with increased verbosity and explicit test paths
# Note: --processes flag removed due to segmentation faults on zLinux (s390x)
# Multiprocessing in nose can cause crashes on certain architectures
# Using --nocapture to see real-time output and identify which test causes segfault
nosetests --exe \
    --verbose \
    --nocapture \
    --with-coverage \
    --cover-xml \
    --cover-xml-file=$coveragedir/.coverage.xml \
    --cover-package=common \
    --cover-package=controllers \
    --with-xunit \
    --xunit-file=$coveragedir/.unitests.xml \
    controllers/tests/ \
    $@

echo "Unit tests completed successfully"
