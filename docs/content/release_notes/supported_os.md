
{{site.data.keyword.attribute-definition-list}}

# Supported operating systems

Kubernetes 1.32 is not supported on Red Hat Enterprise Linux® (RHEL) 8.x.{: restriction}

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on Kubernetes orchestration platforms:

| Operating system                                     |Architecture|
|------------------------------------------------------|------------|
| Red Hat Enterprise Linux® (RHEL) 8.x                 |x86|
| Red Hat Enterprise Linux® (RHEL) 9.x                 |x86|
| Ubuntu 22.04.x LTS                                   |x86|
| Ubuntu 24.04.x LTS                                   |x86|

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on Red Hat OpenShift® orchestration platforms:

| Operating system                                     |Architecture           |
|------------------------------------------------------|-----------------------|
| Red Hat Enterprise Linux CoreOS® (RHCOS) 4.15-4.18   |x86, IBM Z®, IBM Power®|

IBM Power® architecture is only supported with IBM Storage Virtualize® family storage systems.{: restriction}

## Configuration requirements for zLinux

To enable automatic discovery of new luns, it is required to enable the `zfcp.allow_lun_scan` kernel parameter. This can be done with a new machine config, as shown in the example below, after the worker nodes are installed:
```
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: "worker"
  name: 99-worker-kargs-lunscan
spec:
  kernelArguments:
  - 'zfcp.allow_lun_scan=1'
```

Virtualized worker nodes (for example, VMware vSphere) are supported with iSCSI and Fibre Channel (FC) adapters, when the FC adapter is used in passthrough mode.{: tip}

For the latest operating system support information, see the [Lifecycle and support matrix](lifecycle_support_matrix.md).{: tip}

