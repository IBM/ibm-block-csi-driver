#!/bin/bash
set -x
./controller/scripts/pycodestyle.sh
./controller/scripts/pylint.sh
./controller/scripts/unitests.sh
