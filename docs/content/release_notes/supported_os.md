
{{site.data.keyword.attribute-definition-list}}

# Supported operating systems

As of this document's publication date, IBM Power® and zLinux® architectures are not supported for this release. {: restriction}

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on Kubernetes orchestration platforms:

| Operating system                                     |Architecture|
|------------------------------------------------------|------------|
| Red Hat® Enterprise Linux® (RHEL) 8.x                |x86|
| Red Hat® Enterprise Linux® (RHEL) 9.x                |x86|
| Ubuntu 20.04.x LTS                                   |x86|
| Ubuntu 22.04.x LTS                                   |x86|
| Ubuntu 24.04.x LTS                                   |x86|

Kubernetes 1.32 is not supported on Red Hat® Enterprise Linux® (RHEL) 8.x. {: attention}

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on OpenShift orchestration platforms:

| Operating system                                     |Architecture|
|------------------------------------------------------|------------|
| Red Hat® Enterprise Linux CoreOS® (RHCOS) 4.14-4.18  |x86|


Virtualized worker nodes (for example, VMware vSphere) are supported with iSCSI and Fibre Channel (FC) adapters, when the FC adapter is used in passthrough mode. {: tip}

For the latest operating system support information, see the [Lifecycle and support matrix](https://www.ibm.com/docs/en/stg-block-csi-driver?topic=SSRQ8T/landing/csi_lifecycle_support_matrix.html). {: tip}


