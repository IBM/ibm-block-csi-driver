#!/bin/bash -x

kubectl logs $(kubectl get pod -l csi | grep controller | awk '{print$1}') -c ibm-block-csi-controller > /tmp/driver_$(kubectl get pod -l csi | grep controller | awk '{print$1}')_logs.txt
kubectl logs $(kubectl get pod -l csi | grep node | awk '{print$1}') -c ibm-block-csi-node > /tmp/driver_$(kubectl get pod -l csi | grep node | awk '{print$1}')_logs.txt
kubectl logs $(kubectl get pod -l csi | grep operator | awk '{print$1}') > /tmp/driver_$(kubectl get pod -l csi | grep operator | awk '{print$1}')_logs.txt

kubectl describe pod $(kubectl get pod -l csi | grep controller | awk '{print$1}') > /tmp/driver_$(kubectl get pod -l csi | grep controller | awk '{print$1}')_events.txt
kubectl describe pod $(kubectl get pod -l csi | grep node | awk '{print$1}') > /tmp/driver_$(kubectl get pod -l csi | grep node | awk '{print$1}')_events.txt
kubectl describe pod $(kubectl get pod -l csi | grep operator | awk '{print$1}') > /tmp/driver_$(kubectl get pod -l csi | grep operator | awk '{print$1}')_events.txt
