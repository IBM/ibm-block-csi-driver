#!/bin/bash -xe
set +o pipefail

driver_is_ready=false
actual_driver_running_time_in_seconds=0
minimum_driver_running_time_in_seconds=10
while [ "$(kubectl get pod -A -l csi | grep controller | wc -l)" -eq 0 ]; do
  echo "The controller is not deployed"
  sleep 1
done
while [ $driver_is_ready == "false" ]; do
  if [ "$(kubectl get pod -A -l csi | grep -iv running | grep -iv name | wc -l)" -eq 0 ]; then
    ((++actual_driver_running_time_in_seconds))
    if [ $actual_driver_running_time_in_seconds -eq $minimum_driver_running_time_in_seconds ]; then
      driver_is_ready=true
    fi
  else
    actual_driver_running_time_in_seconds=0
  fi
  kubectl get pod -A -l csi
  sleep 1
done
echo Driver is running
kubectl get pod -A -l csi
