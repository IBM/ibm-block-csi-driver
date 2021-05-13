# 1.3.0 (September 2020)

IBM® block storage CSI driver 1.3.0 provided a range of enhancements:

-   New IBM Power Systems™ architecture support for Red Hat® OpenShift® 4.4
-   Additional support for Kubernetes 1.18 and Red Hat OpenShift 4.5 with x86 architecture
-   Supported IBM Z® for Red Hat OpenShift 4.4
-   Enhanced volume mount functionality

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-1672**|Moderate|**Fixed:** In rare cases, if the volume devices have an unexpected `udev` path on the node host, the CSI driver may not be able to find the device mapper in order to mount the volume.|
|**CSI-1658**|Moderate|**Fixed:** In some cases, when mounting a volume through the CSI driver on a Spectrum Virtualize Family system, the same LUN ID may be defined on different I/O groups on the same storage system. This causes the volume mount to fail.|


