#!/bin/bash
set -x
pycodestyle --config="$(dirname "$0")/lint.ini" ./controllers
exit $?
