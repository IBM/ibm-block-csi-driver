# Miscellaneous troubleshooting

Use this information to help pinpoint potential causes for stateful pod failure.

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace   `kubectl`  with `oc` in all relevant commands.

-   [General troubleshooting](#general_troubleshooting)
-   [Error during pod creation](#error_during_pod_creation) \(for volumes using StatefulSet only\)

## General troubleshooting
Use the following command for general troubleshooting:

```
kubectl get -n <namespace>  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi
```

## Error during pod creation
**Note:** This troubleshooting procedure is relevant for volumes using file system types only \(not for volumes using raw block volume types\).

If the following error occurs during stateful application pod creation \(the pod status is _ContainerCreating_):

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

2.  Run the `# multipath -ll` command to see if there are faulty multipath devices.

    If there are faulty multipath devices:

    1.  Restart multipath daemon, using the `systemctl restart multipathd` command.
    2.  Rescan any iSCSI devices, using the `rescan-scsi-bus.sh` command.
    3.  Restart the multipath daemon again, using the `systemctl restart multipathd` command.
    
    The multipath devices should be running properly and the pod should come up immediately.


