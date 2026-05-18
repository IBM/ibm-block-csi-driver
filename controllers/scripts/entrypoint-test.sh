#!/bin/bash

if [[ "$(uname -m)" == "s390x" ]]; then \
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
  fi
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
