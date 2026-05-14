#!/bin/bash
set -xe

coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

ARCH=$(uname -m)

if [ "$ARCH" = "s390x" ]; then
    export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
    export COVERAGE_CORE=pytrace
    WORKERS=0
    USE_SUBPROCESS=1
else
    WORKERS=4
    USE_SUBPROCESS=0
fi

echo "Detected ARCH: $ARCH"
echo "Using WORKERS: $WORKERS"

run_tests() {
    if [ "$WORKERS" -eq 0 ]; then
        DIST_ARGS="-p no:xdist"
    else
        DIST_ARGS="-n $WORKERS"
    fi

    if [ "$USE_SUBPROCESS" -eq 1 ]; then
        # critical for s390x stability
        python - <<PY
import subprocess, sys

cmd = [
    "pytest",
    "--verbose",
    "--exitfirst",
    "--maxfail=1",
    "--capture=no",
    "--tb=short",
    $([ "$WORKERS" -eq 0 ] && echo '"-p","no:xdist",' )
    "--timeout=60",
    "--cov=common",
    "--cov=controllers",
    "--cov-report=xml:$coveragedir/.coverage.xml",
    "--junit-xml=$coveragedir/.unitests.xml",
    "controllers/tests/"
]

result = subprocess.run(cmd)
sys.exit(result.returncode)
PY
    else
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
    fi
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