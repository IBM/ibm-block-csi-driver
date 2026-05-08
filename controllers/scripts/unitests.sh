#!/bin/bash
set -ex

if [ "$TARGETARCH" = "s390x" ]; then \
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
  fi
coveragedir=/driver/coverage/
[ ! -d "$coveragedir" ] && mkdir -p "$coveragedir"

pytest --verbose --capture=no --tb=short -n 4 --timeout=600 \
  --cov=common --cov=controllers \
  --cov-report=xml:/driver/coverage/.coverage.xml \
  --junit-xml=/driver/coverage/.unitests.xml \
  controllers/tests/
