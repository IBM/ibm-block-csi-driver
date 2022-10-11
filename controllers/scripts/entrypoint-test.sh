#!/bin/bash

./controllers/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controllers/scripts/pylint.sh
PYLINT=$?

./controllers/scripts/unitests.sh
UNITESTS=$?

echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"
while true; do sleep 1000; done