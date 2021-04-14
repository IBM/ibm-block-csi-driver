#!/bin/bash
set -x

./controller/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controller/scripts/pylint.sh
PYLINT=$?

./controller/scripts/unitests.sh
UNITESTS=$?

set +x
echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"