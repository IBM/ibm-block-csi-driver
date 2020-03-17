#!/bin/bash -x
pycodestyle --max-line-length 120 --exclude=controller/csi_general --ignore=E402 ./controller
exit $?
