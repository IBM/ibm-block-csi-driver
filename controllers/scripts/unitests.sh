#!/bin/bash
set -xe
if [[ "$(uname -m)" == "s390x" ]]; then
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
fi
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

# -n 4: Run tests in parallel using 4 processes (equivalent to nose --processes=4)
# --timeout=600: Set test timeout to 600 seconds (equivalent to nose --process-timeout=600)
# --exitfirst = stop running on first failure
timeout 120 pytest \
    --verbose \
    --exitfirst \
    --maxfail=1 \
    --capture=no \
    --tb=short \
    -n 4 \
    --timeout=60 \
    --cov=common \
    --cov=controllers \
    --cov-report=xml:$coveragedir/.coverage.xml \
    --junit-xml=$coveragedir/.unitests.xml \
    controllers/tests/ \
    $@

echo "Unit tests completed successfully"
