# Supported operating systems

The following table lists operating systems required for deployment of the IBM® block storage CSI driver.

|Operating system|Architecture|
|----------------|------------|
|Red Hat® Enterprise Linux® (RHEL) 7.x|x86, IBM Z®|
|Red Hat® Enterprise Linux® (RHEL) 8.x|x86|
|Red Hat Enterprise Linux CoreOS (RHCOS) 4.10-4.12|x86, IBM Z, IBM Power Systems™<sup>1</sup>|
|Ubuntu 20.04.4 LTS<sup>2</sup>| x86_64|

<sup>1</sup>IBM Power Systems architecture is only supported on Spectrum Virtualize and DS8000 family storage systems.<br/>
<sup>2</sup>Ubuntu is supported with Kubernetes orchestration platforms only.

**Note:** 
- Virtualized worker nodes (for example, VMware vSphere) are supported with iSCSI and Fibre Channel (FC) adapters, when the FC adapter is used in passthrough mode.
- For the latest operating system support information, see the [Lifecycle and support matrix](https://www.ibm.com/docs/en/stg-block-csi-driver?topic=SSRQ8T/landing/csi_lifecycle_support_matrix.html).


