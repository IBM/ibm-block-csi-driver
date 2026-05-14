#!/bin/bash
set -euo pipefail

coveragedir=/driver/coverage/
mkdir -p "$coveragedir"

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
    local rc=0

    if [ "$WORKERS" -eq 0 ]; then
        DIST_ARGS="-p no:xdist"
    else
        DIST_ARGS="-n $WORKERS"
    fi

    if [ "$USE_SUBPROCESS" -eq 1 ]; then
        # s390x-safe execution via python subprocess
        python - <<'PY'
import subprocess, sys

cmd = [
    "pytest",
    "--verbose",
    "--exitfirst",
    "--maxfail=1",
    "--capture=no",
    "--tb=short",
    "-p", "no:xdist",
    "--timeout=60",
    "--cov=common",
    "--cov=controllers",
    "--cov-report=xml:$coveragedir/.coverage.xml",
    "--junit-xml=$coveragedir/.unitests.xml",
    "controllers/tests/"
]

result = subprocess.run(cmd)
print("PYTEST EXIT CODE:", result.returncode)
sys.exit(result.returncode)
PY
        rc=$?
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
            controllers/tests/
        rc=$?
    fi

    return $rc
}

MAX_FAILURES=3
failures=0
attempt=0

while true; do
    attempt=$((attempt + 1))
    echo "[Attempt $attempt] Running unit tests..."

    run_tests
    rc=$?

    if [ $rc -eq 0 ]; then
        echo "[Attempt $attempt] Unit tests completed successfully."
        break
    fi

    failures=$((failures + 1))
    echo "[Attempt $attempt] Tests FAILED (failure $failures of $MAX_FAILURES). Exit code: $rc"

    if [ $failures -ge $MAX_FAILURES ]; then
        echo "Maximum failures ($MAX_FAILURES) reached. Aborting."
        exit $rc
    fi

    echo "Sleeping 60 seconds before retry..."
    sleep 60
done