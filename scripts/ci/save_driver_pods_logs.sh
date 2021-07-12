#!/bin/bash -x

get_pod_logs_or_events (){
    pod_name=$1
    information_type=$2
    kubectl $information_type $(kubectl get pod -l csi | grep $pod_name | awk '{print$1}') -c ibm-block-csi-$pod_name > /tmp/driver_$(kubectl get pod -l csi | grep $pod_name | awk '{print$1}')_$information_type.txt
}

declare -a pod_types=(
    "node"
    "controller"
    "operator"
)

for pod_type in "${pod_types[@]}"
do
    get_pod_logs_or_events $pod_type logs
    get_pod_logs_or_events $pod_type describe
done
