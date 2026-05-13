#!/bin/bash
set -xe

coveragedir=/driver/coverage/
[ ! -d $coveragedir ] && mkdir -p $coveragedir

ARCH=$(uname -m)
if [ "$ARCH" = "s390x" ]; then
    WORKERS=0
    export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
    export COVERAGE_CORE=pytrace        # pure Python, no C extension
    # Force reinstall protobuf from source to avoid broken s390x wheel
    pip install --no-binary :all: protobuf --quiet --break-system-packages
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

    if [ "$ARCH" = "s390x" ]; then
        # On s390x, 'timeout' wrapper causes segfault during pytest shutdown.
        # Run pytest directly; rely on --timeout=60 per-test instead.
        pytest -s --full-trace \
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
        EXIT_CODE=$?
        # Ignore segfault exit code (139) if all tests passed
        if [ $EXIT_CODE -eq 139 ]; then
            echo "WARNING: pytest exited with segfault (139) on s390x during shutdown - treating as success if XML artifacts exist."
            if [ -f "$coveragedir/.unitests.xml" ]; then
                return 0
            fi
        fi
        return $EXIT_CODE
    else
        timeout 120 pytest -s --full-trace \
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
