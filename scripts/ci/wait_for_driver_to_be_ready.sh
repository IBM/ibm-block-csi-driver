#!/bin/bash -xe
set +o pipefail

driver_is_ready=false
amount_of_seconds_that_driver_pods_are_running=0
amount_of_seconds_that_driver_pods_should_be_running=10
while [ "$(kubectl get pod -A -l csi | grep controller | wc -l)" -eq 0 ]; do
  echo "The controller is not deployed"
  sleep 1
done
while [ $driver_is_ready == "false" ]; do
  if [ "$(kubectl get pod -A -l csi | grep -iv running | grep -iv name | wc -l)" -eq 0 ]; then
    ((amount_of_seconds_that_driver_pods_are_running=amount_of_seconds_that_driver_pods_are_running+1))
    if [ $amount_of_seconds_that_driver_pods_are_running -eq $amount_of_seconds_that_driver_pods_should_be_running ]; then
      driver_is_ready=true
    fi
  else
    amount_of_seconds_that_driver_pods_are_running=0
  fi
  kubectl get pod -A -l csi
  sleep 1
done
echo Driver is running
kubectl get pod -A -l csi
