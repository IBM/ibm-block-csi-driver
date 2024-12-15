# Supported operating systems

The following table lists operating systems required for deployment of the IBM® block storage CSI driver.

| Operating system                                     |Architecture|
|------------------------------------------------------|------------|
| Red Hat® Enterprise Linux® (RHEL) 8.x                |x86<sup>2</sup>|
| Red Hat® Enterprise Linux® (RHEL) 9.x                |x86<sup>2</sup>|
| Red Hat® Enterprise Linux CoreOS® (RHCOS) 4.14-4.17  |x86|
| Ubuntu 20.04.x LTS                                   |x86<sup>1</sup>|
| Ubuntu 22.04.x LTS                                   |x86<sup>1</sup>|
| Ubuntu 24.04.x LTS                                   |x86<sup>1</sup>|

<sup>1</sup>Ubuntu is supported with Kubernetes orchestration platforms only.<br/>
<sup>2</sup>Red Hat® Enterprise Linux® (RHEL) 8.x and 9.x are supported with Kubernetes orchestration platforms only.<br/>

**Note:** 
- Virtualized worker nodes (for example, VMware vSphere) are supported with iSCSI and Fibre Channel (FC) adapters, when the FC adapter is used in passthrough mode.
- For the latest operating system support information, see the [Lifecycle and support matrix](https://www.ibm.com/docs/en/stg-block-csi-driver?topic=SSRQ8T/landing/csi_lifecycle_support_matrix.html).
- As of this document's publication date, IBM Power® and zLinux® architectures are not supported for this release.


