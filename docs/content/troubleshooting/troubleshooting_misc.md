
{{site.data.keyword.attribute-definition-list}}

# Miscellaneous troubleshooting

Use this information to help pinpoint potential causes for stateful pod failure.

These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.{: tip}

## General troubleshooting
Use the following command for general troubleshooting:

```
kubectl get -n <namespace>  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
```

## Error during pod creation

This troubleshooting procedure is relevant for volumes using file system volume mode only (not for volumes using raw block volume mode).{: attention}

If the following error occurs during stateful application pod creation (the pod status is _ContainerCreating_):

```screen
    -8e73-005056a49b44" : rpc error: code = Internal desc = 'fsck' found errors on device /dev/dm-26 but could not correct them: fsck from util-linux 2.23.2
    /dev/mapper/mpathym: One or more block group descriptor checksums are invalid. FIXED.
    /dev/mapper/mpathym: Group descriptor 0 checksum is 0x0000, should be 0x3baa.
    
    /dev/mapper/mpathym: UNEXPECTED INCONSISTENCY; RUN fsck MANUALLY.
    (i.e., without -a or -p options)
```


1.  Log in to the relevant worker node and run the `fsck` command to repair the filesystem manually.

    `fsck /dev/dm-<X>`

    The pod should come up immediately. If the pod is still in a _ContainerCreating_ state, continue to the next step.

2.  Run the `multipath -ll` command to see if there are faulty multipath devices.

    If there are faulty multipath devices:

    1.  Restart multipath daemon, using the `systemctl restart multipathd` command.
    2.  Rescan any iSCSI devices, using the `rescan-scsi-bus.sh` command.
    3.  Restart the multipath daemon again, using the `systemctl restart multipathd` command.
    
    The multipath devices should be running properly and the pod should come up immediately.

## Error during PVC expansion

This troubleshooting procedure is a workaround for issue **CSI-5769** (see [Known issues](../release_notes/known_issues.md)){: note}

If the PVC expansion fails with the following event

``` screen
      Type                      Status  LastProbeTime                     LastTransitionTime                Reason  Message
      ----                      ------  -----------------                 ------------------                ------  -------
      FileSystemResizePending   True    Mon, 01 Jan 0001 00:00:00 +0000   Tue, 14 Jan 2025 15:36:16 +0000           Waiting for user to (re-)start a pod to finish file system resize of volume on node.
```

1. Examine the logs of the IBM Block Storage CSI driver node pods. Check if there are log lines similar to the following

``` screen
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (node.go:732) - Discovered device : {/dev/dm-2}
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (node_utils.go:168) - GetSysDevicesFromMpath with param : {dm-2}
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (node_utils.go:170) - looking in path : {/sys/block/dm-2/slaves}
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (node_utils.go:177) - found slaves : {[0xc000586340 0xc000586410]}
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (executer.go:75) - Executing command : {nvme} with args : {[list]}. and timeout : {10000} mseconds
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (executer.go:69) - Non-zero exit code: exit status 1
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (executer.go:86) - Finished executing command (no output)
    2025-01-20 15:43:49,1203 ERROR	[639] [SVC:100;6005076810830237180000000000038F] (node.go:744) - Error while trying to check if sys devices are nvme devices : {exit status 1}
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (sync_lock.go:62) - Lock for action NodeExpandVolume, release lock for volume
    2025-01-20 15:43:49,1203 DEBUG	[639] [SVC:100;6005076810830237180000000000038F] (node.go:745) - <<<< NodeExpandVolume
    2025-01-20 15:43:49,1203 ERROR	[639] [-] (driver.go:85) - GRPC error: rpc error: code = Internal desc = exit status 1
```


2. Check if required NVMe kernel modules are loaded with command `lsmod | grep nvme`. Kernel modules `nvme` and `nvme_core` are required.

3. If the required kernel modules `nvme` and `nvme_core` are not loaded, manually load them with command `modprobe nvme; modprove nvme_core` and then rerun PVC expansion.
