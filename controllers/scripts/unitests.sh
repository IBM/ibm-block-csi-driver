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
    EXIT_CODE=$?

    if [ "$ARCH" = "s390x" ] && [ $EXIT_CODE -eq 139 ]; then
        echo "s390x: caught exit 139 (segfault during pytest shutdown), checking test results..."

        # Confirm XML exists
        if [ ! -f "$coveragedir/.unitests.xml" ]; then
            echo "FAIL: no JUnit XML found — pytest likely crashed before completing."
            return 1
        fi

        # Check for failures/errors in the XML
        FAILURES=$(python3 -c "
import xml.etree.ElementTree as ET
tree = ET.parse('$coveragedir/.unitests.xml')
root = tree.getroot()
suite = root if root.tag == 'testsuite' else root.find('testsuite')
failures = int(suite.attrib.get('failures', 1))
errors   = int(suite.attrib.get('errors',   1))
print(failures + errors)
")

        if [ "$FAILURES" -eq 0 ]; then
            echo "s390x: all tests passed. Ignoring post-run segfault."
            return 0
        else
            echo "FAIL: JUnit XML reports $FAILURES failure(s)/error(s). Real test failure."
            return 1
        fi
    fi

    return $EXIT_CODE
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
