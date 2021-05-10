# 1.5.0 (March 2021)

IBM® block storage CSI driver 1.5.0 provided a range of enhancements and resolved the following issues:

-   New support for IBM Cloud® Satellite
-   Added automatic seamless upgrade capabilities
-   Additional support for Kubernetes 1.20 and Red Hat® OpenShift® 4.7 with x86 architecture
-   Added Red Hat OpenShift web console installation support for IBM Z® and IBM Power® Systems

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-2487**|Moderate|**Fixed:** In some cases when using RedHat OpenShift or K8S orchestration platform, some secrets are deleted due to kubernetes issue which causes pods to fail or stuck in "ContainerCreating" state.|
|**CSI-2471**|Service|**Fixed:** In some cases, during volume expansion of an XFS file system, the `xfs_growfs` command may fail on some operating system versions. In these cases, the following log messages will show in the relevant IBM block storage CSI driver node logs:<br /><pre>(node.go:646) - Could not resize {xfs} file system of {/dev/<device>} , error: exit status 1<br /> (sync_lock.go:62) - Lock for action NodeExpandVolume, release lock for volume<br />(driver.go:85) - GRPC error: rpc error: code = Internal desc = exit status 1</pre>|



