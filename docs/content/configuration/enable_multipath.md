# Enable multipath

Multipath must be enabled on the Kubernetes or RedHat OpenShift cluster nodes, otherwise pod creation may be stuck due to PVC mount failure.
Such failures can be detected in the `Events` section of the `kubectl describe pod <pod>` output

```
Events:
  Type     Reason                  Age                 From                     Message
  ----     ------                  ----                ----                     -------
  Normal   SuccessfulAttachVolume  114s                attachdetach-controller  AttachVolume.Attach succeeded for volume <pvc uuid>
  Warning  FailedMount             43s (x8 over 110s)  kubelet                  MountVolume.MountDevice failed for volume <pvc uuid> : rpc error: code = Internal desc = exit status 1
```

To debug the issue, review the IBM Block Storage CSI driver node pod logs, e.g.

```
oc logs ibm-block-csi-node-wz4rk -n openshift-storage
```


Look in the logs for (the exact output may change for future Kubernetes/RedHat OpenShift versions):

```
2024-03-10 11:03:42,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:75) - Executing command : {multipath} with args : {[-r]}. and timeout : {60000} mseconds
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:82) - Output from command: 343111.597184 | /etc/multipath.conf does not exist, blacklisting all devices.
343111.597247 | You can run "/sbin/mpathconf --enable" to create
343111.597257 | /etc/multipath.conf. See man mpathconf(8) for more details2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:86) - Finished executing command
2024-03-10 11:03:43,31011 INFO    [281680] [SVC:14;600507680C8006BE780000000000001C] (device_connectivity_helper_scsigeneric.go:574) - ReloadMultipath: reload finished successfully
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (device_connectivity_helper_scsigeneric.go:675) - Waiting for dm to exist
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:75) - Executing command : {multipathd} with args : {[show maps raw format " %w,%d "]}. and timeout : {10000} mseconds
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:69) - Non-zero exit code: exit status 1
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:82) - Output from command: 343111.815423 | /etc/multipath.conf does not exist, blacklisting all devices.
343111.815496 | You can run "/sbin/mpathconf --enable" to create
343111.815506 | /etc/multipath.conf. See man mpathconf(8) for more details2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (executer.go:86) - Finished executing command
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (node.go:150) - Discovered device : {}
2024-03-10 11:03:43,31011 ERROR    [281680] [SVC:14;600507680C8006BE780000000000001C] (node.go:152) - Error while discovering the device : {exit status 1}
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (sync_lock.go:62) - Lock for action NodeStageVolume, release lock for volume
2024-03-10 11:03:43,31011 DEBUG    [281680] [SVC:14;600507680C8006BE780000000000001C] (node.go:153) - <<<< NodeStageVolume
2024-03-10 11:03:43,31011 ERROR    [281680] [-] (driver.go:85) - GRPC error: rpc error: code = Internal desc = exit status 1
```


To enable multipath, log into the appropriate worker nodes (On RedHat OpenShift using the `oc debug` command) and enable multipath:
```
sh-5.1# mpathconf --enable
sh-5.1# systemctl start multipathd.service
sh-5.1# multipath -ll
mpathh (3600507680c8006be780000000000001c) dm-7 IBM,2145
size=40G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| |- 33:0:10:83  sdbu 68:128 active ready running
| `- 33:0:7:83   sdbs 68:96  active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  |- 33:0:1:83   sdbr 68:80  active ready running
  `- 33:0:8:83   sdbt 68:112 active ready running
```

Confirm the pod is running with "kubectl get -A pod -o wide | grep ibm-block-csi" and that the device is mounted in the container
