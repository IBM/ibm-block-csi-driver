#!/bin/bash

./controller/scripts/csi_pb2.sh
CSI_PB2=$?

./controller/scripts/pycodestyle.sh
PYCODESTYLE=$?

./controller/scripts/pylint.sh
PYLINT=$?

./controller/scripts/unitests.sh
UNITESTS=$?

echo "-------- Summary of exit codes --------"
echo "csi_pb2: $CSI_PB2"
echo "pycodestyle: $PYCODESTYLE"
echo "pylint: $PYLINT"
echo "unitests: $UNITESTS"
