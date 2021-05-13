# 1.4.0 (December 2020)

IBM® block storage CSI driver 1.4.0 provided a range of enhancements and resolved the following issues:

-   New IBM Power Systems™ architecture support for Red Hat® OpenShift® 4.4 and 4.5
-   Additional support for Kubernetes 1.19 and Red Hat OpenShift 4.4 and 4.5 with x86 architecture
-   Supports for IBM Z® for Red Hat OpenShift 4.5
-   Enhanced volume functionality

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-2156**|Service|**Fixed:** IBM block storage driver node registration may encounter an error when the `node_id` exceeds 128 bytes.|
|**CSI-1842**|Service|**Fixed:** When creating a new volume on a DS8000® Family storage system, if an error occurs during PersistentVolumeClaim \(PVC\) attachment, the attachment retry may fail.|
|**CSI-645**|Low|**Fixed:** In some cases, during high-scale operations, such as pod creation with many PersistentVolumeClaims \(PVCs\), the "ibm-block-csi-controller-0" controller pod restarts.|


