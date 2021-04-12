# 1.5.0 \(March 2021\)

IBM® block storage CSI driver 1.5.0 provides a range of enhancements and resolves the following issues:

-   color:blue;New support for IBM Cloud® Satellite
-   color:blue;Added automatic seamless upgrade capabilities
-   color:blue;Additional support for Kubernetes 1.20 and Red Hat® OpenShift® 4.7 with x86 architecture
-   color:blue;Added Red Hat OpenShift web console installation support for IBM Z® and IBM Power® Systems

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**color:blue;CSI-2487**|Moderate|**Fixed:** In some cases when using RedHat OpenShift or K8S orchestration platform, some secrets are deleted due to kubernetes issue which causes pods to fail or stuck in "ContainerCreating" state.|
|**color:blue;CSI-2471**|color:blue;Service|color:blue;**Fixed:** In some cases, during volume expansion of an XFS file system, the xfs\_growfs command may fail on some operating system versions. In these cases, the following log messages will show in the relevant IBM block storage CSI driver node logs:```
(node.go:646) - Could not resize {xfs} file system of {/dev/<device>} , error: exit status 1
(sync_lock.go:62) - Lock for action NodeExpandVolume, release lock for volume
(driver.go:85) - GRPC error: rpc error: code = Internal desc = exit status 1
```

|

**Parent topic:**[Change log](csi_rn_changelog.md)

