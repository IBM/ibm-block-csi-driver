#!/bin/bash

./controllers/servers/csi/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controllers/servers/csi/scripts/pylint.sh
PYLINT=$?

./controllers/servers/csi/scripts/unitests.sh
UNITESTS=$?

echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"
