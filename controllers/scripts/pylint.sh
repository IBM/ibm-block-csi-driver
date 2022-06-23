#!/bin/bash
set -x
pylint --rcfile="$(dirname "$0")/lint.ini" ./controllers
exit $?
