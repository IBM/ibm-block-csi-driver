#!/bin/bash
set -xe
coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

# Use pytest instead of nose (nose is unmaintained and has issues on s390x)
# pytest is more stable and better maintained
# -n 4: Run tests in parallel using 4 processes (equivalent to nose --processes=4)
# --timeout=600: Set test timeout to 600 seconds (equivalent to nose --process-timeout=600)
pytest \
    --verbose \
    --capture=no \
    --tb=short \
    -n 4 \
    --timeout=600 \
    --cov=common \
    --cov=controllers \
    --cov-report=xml:$coveragedir/.coverage.xml \
    --junit-xml=$coveragedir/.unitests.xml \
    controllers/tests/ \
    $@

echo "Unit tests completed successfully"
