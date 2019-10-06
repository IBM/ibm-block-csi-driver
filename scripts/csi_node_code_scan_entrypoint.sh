#!/bin/bash

if [ -z "$EXCLUDE" ]
then
    gosec -fmt=junit-xml -out /results/gosec_result.xml /node
else
    gosec -exclude=${EXCLUDE} -fmt=junit-xml -out /results/gosec_result.xml /node
fi

