
{{site.data.keyword.attribute-definition-list}}

# Supported operating systems

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on Kubernetes orchestration platforms:

| Operating system                                     |Architecture|
|------------------------------------------------------|------------|
| Red Hat Enterprise Linux® (RHEL) 9.x                 |x86|
| Red Hat Enterprise Linux® (RHEL) 10.x                |x86|
| Ubuntu 22.04.x LTS                                   |x86|
| Ubuntu 24.04.x LTS                                   |x86|
| Ubuntu 26.04.x LTS                                   |x86|
| SUSE Linux Enterprise Server (SLES) 15, 16           |x86|

The following table lists supported operating systems for deployment of the IBM® block storage CSI driver on Red Hat OpenShift® orchestration platforms:

| Operating system                                     |Architecture           |
|------------------------------------------------------|-----------------------|
| Red Hat Enterprise Linux CoreOS® (RHCOS) 4.19-4.22   |x86, IBM Z®, IBM Power®|

IBM Power® architecture is only supported with IBM Storage Virtualize® family storage systems.{: restriction}

## Configuration requirements for zLinux

Fibre Channel (FC) connectivity on zLinux requires both of the following prerequisites:

- **Automatic LUN discovery (`allow_lun_scan`)**: To enable automatic discovery of new luns, it is required to enable the `zfcp.allow_lun_scan` kernel parameter. This can be done with a new machine config, as shown in the example below, after the worker nodes are installed:
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
- **NPIV on the FCP adapter**: The FCP adapter assigned to the LPAR must operate as an NPIV virtual port (`port_type: NPIV VPORT`). Automatic LUN discovery does not work if NPIV is not active on the FCP adapter, even when `allow_lun_scan` is set to `Y`. If NPIV is not enabled, enable it for the FCP adapter in the HMC/LPAR configuration.

To verify the prerequisites on a worker node, run the following commands:

1. Verify that automatic LUN scan is enabled. The expected output is `Y`:
```
cat /sys/module/zfcp/parameters/allow_lun_scan
```
2. Verify that NPIV is active on the FCP adapter. The expected output is `port_type: NPIV VPORT`. If `port_type: NPort (fabric via point-to-point)` is returned, NPIV is not active:
```
lszfcp -Ha | grep -e port_type
```

If a volume cannot be attached because the multipath device is not found, the CSI node plugin checks these settings and appends the detected values and remediation guidance to the error message.

Virtualized worker nodes (for example, VMware vSphere) are supported with iSCSI and Fibre Channel (FC) adapters, when the FC adapter is used in passthrough mode.{: tip}

For the latest operating system support information, see the [Lifecycle and support matrix](lifecycle_support_matrix.md).{: tip}

