# Supported orchestration platforms

The following table details orchestration platforms suitable for deployment of the IBM® block storage CSI driver.

|Orchestration platform| Version |Architecture|
|----------------------|---------|------------|
|Kubernetes<sup>2</sup>| 1.24    |x86|
|Kubernetes<sup>2</sup>| 1.25    |x86|
|Kubernetes<sup>2</sup>| 1.26    |x86|
|Kubernetes<sup>2</sup>| 1.27    |x86|
|Red Hat OpenShift| 4.10    |x86, IBM Z, IBM Power Systems<sup>1</sup>|
|Red Hat OpenShift| 4.11    |x86, IBM Z, IBM Power Systems<sup>1</sup>|
|Red Hat OpenShift| 4.12    |x86, IBM Z, IBM Power Systems<sup>1</sup>|
|Red Hat OpenShift| 4.13    |x86, IBM Z, IBM Power Systems<sup>1</sup>|

<sup>1</sup> IBM Power Systems architecture is only supported on Spectrum Virtualize and DS8000 family storage systems.
<sup>2</sup> [MicroK8s](https://microk8s.io/) is not supported for use with IBM® block storage CSI driver.

**Note:** 
- As of this document's publication date, IBM Cloud® Satellite only supports RHEL 7 on x86 architecture for Red Hat OpenShift. For the latest support information, see [cloud.ibm.com/docs/satellite](https://cloud.ibm.com/docs/satellite).
- For the latest orchestration platform support information, see the [Lifecycle and support matrix](https://www.ibm.com/docs/en/stg-block-csi-driver?topic=SSRQ8T/landing/csi_lifecycle_support_matrix.html).
