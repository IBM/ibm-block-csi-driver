#!/bin/bash
set -ex

export COVERAGE_NO_CTRACER=1

coveragedir=/driver/coverage/
[ ! -d "$coveragedir" ] && mkdir -p "$coveragedir"

exec pytest \
  --verbose \
  --capture=no \
  --tb=short \
  --cov=common \
  --cov=controllers \
  --cov-report=xml:$coveragedir/.coverage.xml \
  --junit-xml=$coveragedir/.unitests.xml \
  controllers/tests/ "$@"