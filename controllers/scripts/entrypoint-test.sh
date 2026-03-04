#!/bin/bash

./controllers/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controllers/scripts/pylint.sh
PYLINT=$?


echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
