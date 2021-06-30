#!/bin/bash -xe
set +o pipefail

echo "Wait for all the pods to be in running state"
while [ "$(kubectl get pod -A | grep 0/ | grep -iv name | wc -l)" -gt 0 ]; do
  echo Some pods did not start thier containers
  kubectl get pod -A | grep 0/ | grep -iv name
  sleep 5
done
while [ "$(kubectl get pod -A | grep -iv running | grep -iv name | wc -l)" -gt 0 ]; do
  echo Some pods are still not in running state
  kubectl get pod -A | grep -iv running | grep -iv name
  sleep 5
done
echo Cluster is ready
kubectl get pod -A
