#!/bin/bash -x

run_action_on_pod (){
    pod_name=$1
    action=$2
    extra_args=$3
    get_all_pods_by_type="kubectl get pod -l csi | grep $pod_name | awk '{print$1}'"
    kubectl $action $($get_all_pods_by_type) $extra_args -c ibm-block-csi-$pod_name > /tmp/driver_$($get_all_pods_by_type)_$action.txt
}

declare -a pod_types=(
    "node"
    "controller"
    "operator"
)

for pod_type in "${pod_types[@]}"
do
    run_action_on_pod $pod_type logs "-c ibm-block-csi-$pod_name"
    run_action_on_pod $pod_type describe ""
done
