#!/bin/bash

./controllers/csi_server/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controllers/csi_server/scripts/pylint.sh
PYLINT=$?

./controllers/csi_server/scripts/unitests.sh
UNITESTS=$?

echo "-------- Summary of exit codes --------"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"
