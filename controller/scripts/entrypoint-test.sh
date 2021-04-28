#!/bin/bash

./controller/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controller/scripts/pylint.sh
PYLINT=$?

./controller/scripts/unitests.sh
UNITESTS=$?

echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"
