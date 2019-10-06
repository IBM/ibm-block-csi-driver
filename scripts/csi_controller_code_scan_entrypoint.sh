#!/bin/bash

if [ -z "$SKIPS" ]
then
    /usr/local/bin/bandit -ll -f xml -o /results/bandit_result.xml -r /controller
else
    /usr/local/bin/bandit -ll -s ${SKIPS} -f xml -o /results/bandit_result.xml -r /controller
fi

