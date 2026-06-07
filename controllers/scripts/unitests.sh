#!/bin/bash
set -xe
if [[ "$(uname -m)" == "s390x" ]]; then
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
fi
coveragedir=/driver/coverage/
[[ ! -d "$coveragedir" ]] && mkdir -p "$coveragedir"

run_tests() {
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
        "$@"

    local exit_code=$?
    # exit_codes:
    # 124 = timeout expired
    # 137 = killed by signal
    return $exit_code
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

        if [[ $failures -ge $MAX_FAILURES ]]; then
            echo "Maximum failures ($MAX_FAILURES) reached. Aborting."
            exit 1
        fi

        echo "Sleeping 60 seconds before retry..."
        sleep 60
    fi
done
