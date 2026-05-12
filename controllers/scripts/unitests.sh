#!/bin/bash
set -xe

coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

ARCH=$(uname -m)
if [ "$ARCH" = "s390x" ]; then
    WORKERS=0
    export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
else
    WORKERS=4
fi
echo "Detected ARCH: $ARCH"
echo "Using WORKERS: $WORKERS"

run_tests() {
    if [ "$WORKERS" -eq 0 ]; then
        DIST_ARGS="-p no:xdist"
    else
        DIST_ARGS="-n $WORKERS"
    fi
    timeout 120 pytest \
        --verbose \
        --exitfirst \
        --maxfail=1 \
        --capture=no \
        --tb=short \
        $DIST_ARGS \
        --timeout=60 \
        --cov=common \
        --cov=controllers \
        --cov-report=xml:$coveragedir/.coverage.xml \
        --junit-xml=$coveragedir/.unitests.xml \
        controllers/tests/ \
        "$@"
}

MAX_FAILURES=3
failures=0
attempt=0

while true; do
    attempt=$((attempt + 1))
    echo "[Attempt $attempt] Running unit tests..."

    if run_tests "$@"; then
        echo "[Attempt $attempt] Unit tests completed successfully."
        break
    else
        failures=$((failures + 1))
        echo "[Attempt $attempt] Tests FAILED (failure $failures of $MAX_FAILURES)."

        if [ $failures -ge $MAX_FAILURES ]; then
            echo "Maximum failures ($MAX_FAILURES) reached. Aborting."
            exit 1
        fi

        echo "Sleeping 60 seconds before retry..."
        sleep 60
    fi
done
