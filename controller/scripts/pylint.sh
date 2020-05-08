#!/bin/bash -x
pylint --errors-only --disable=no-value-for-parameter,no-member --ignore=csi_general ./controller
exit $?
