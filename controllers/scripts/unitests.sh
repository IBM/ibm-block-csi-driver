#!/bin/bash
set -ex

coveragedir=/driver/coverage/
[ ! -d "$coveragedir" ] && mkdir -p "$coveragedir"

pytest --verbose --capture=no --tb=short -n 4 --timeout=600 --cov=common --cov=controllers --cov-report=xml:/driver/coverage//.coverage.xml --junit-xml=/driver/coverage//.unitests.xml controllers/tests/
