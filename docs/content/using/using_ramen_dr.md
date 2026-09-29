# Ramen

Ramen is an
[open-cluster-management (OCM)](https://open-cluster-management.io/docs/concepts/architecture/)
[placement](https://open-cluster-management.io/docs/concepts/content-placement/placement/)
extension that provides **Kubernetes-native Disaster Recovery** for
[workloads](https://kubernetes.io/docs/concepts/workloads/) and their persistent
data across a pair of OCM managed clusters. Ramen orchestrates, workload
protection and placement on managed clusters through:

- **Relocate**: Planned migration to a peer cluster for maintenance,
  optimization, or failback

- **Failover**: Unplanned recovery to a peer cluster after cluster loss or
  failure

## Persistent Data Protection
Ramen supports several approaches for replicating persistent data across clusters, including **Storage Vendor Assisted Replication**, which is supported by the IBM Block CSI Driver.

### Storage Vendor Assisted Replication
Ramen uses storage plugins to orchestrate persistent data replication and recovery across clusters. For the **IBM Block CSI Driver**, Ramen uses the CSI [Storage Replication Specification](https://github.com/csi-addons/spec/tree/main/replication) and `VolumeReplication` [APIs](https://github.com/csiblock/volume-replication-operator/tree/main/api/v1alpha1) to orchestrate volume replication and recovery.

## Workload Resource Protection
Ramen supports several approaches for protecting and replicating application resources across clusters. For more information, refer to the [Ramen Workload Resource Protection documentation](https://github.com/RamenDR/ramen/blob/main/README.md#workload-resource-protection).

## Requirements

- **Container Orchestration Platform:** Kubernetes **1.30 or above**, or OpenShift
  Container Platform (OCP) **4.18 or above**

- **IBM Block CSI Driver:** Version **1.14.0 or above**

- **IBM Storage Virtualize (SVC):** Version **9.2.0.0 or above**, which supports the
  `remote_volume_uid` field in `lsvdisk`


## Installation and Configuration

- **Ramen:** For installation, configuration, and usage instructions, refer to the
  [Ramen documentation](https://github.com/RamenDR/ramen/blob/main/README.md#getting-started).

- **IBM Block CSI Driver:** For installation, configuration, and usage instructions,
  refer to the
  [IBM Block CSI Driver 1.14.0 documentation](https://www.ibm.com/docs/en/stg-block-csi-driver/1.14.0).

## How Ramen Replication Works with IBM Block CSI Driver
The IBM Block CSI Driver supports Ramen **Regional DR (Async)** using Enhanced
Asynchronous Replication (EAR), which replicates at the **Volume Group (VG) level**.
Replication is supported for both VGs **inside a storage partition** and VGs
**outside a storage partition**.

### Replication flow
Ramen replicates a group of volumes together using the `VolumeGroupReplication`
API. Since the IBM Block CSI Driver supports only `VolumeReplication`, Ramen
relies on it instead:

1. Ramen creates a separate `VolumeReplication` for each PVC selected by the DRPC,
   so from Ramen's point of view, data is protected at the **volume (PVC) level**.
2. When the CSI Driver receives a `VolumeReplication` request, it identifies the
   VG that the PVC belongs to.
3. The CSI Driver performs the DR operation, such as promote or demote, on the
   **entire VG** on the storage system.

### PVC selection rule
- **All PVCs in a VG must be included:** A single DRPC can protect PVCs from
  **one or more VGs**, but when a VG is included, **all PVCs in that VG must be
  included** in the same DRPC. Selecting only some PVCs from a VG is not supported.

- **Impact of excluding a PVC:** If a PVC from the VG is left out of the DRPC, the
  storage still replicates it along with the rest of the VG, but Ramen does not
  track it. Its `VolumeReplication` state in Kubernetes may then not match its
  actual replication state on the storage system.

## Replication Scope: VG Inside vs Outside a Storage Partition
Replication is supported for a VG **outside a storage partition** and a VG
**inside a storage partition**. The configuration differs as follows:

| | VG outside a partition | VG inside a partition |
|---|---|---|
| **Storage setup** | Partnership between the two storage systems. | A partition on each storage system, with a DR link configured between the two partitions. |
| **Replication policy** | Topology `async-dr`. Create it on the primary storage system; it is created automatically on the secondary. | Topology `2-site-async-dr`. Create it in the partition on **each** storage system. |
| **Secret** | No partition field. | Must include the `partition_name` field. |

## Using Ramen with IBM Block CSI Driver
The following example protects the PVCs of a workload in the `<namespace>`
namespace across two managed clusters, `<managed-cluster-1>` and
`<managed-cluster-2>`.

The VG used in this example is **outside a storage partition**. For a VG inside a
storage partition, apply the differences described in
[Replication Scope: VG Inside vs Outside a Storage Partition](#replication-scope-vg-inside-vs-outside-a-storage-partition).

---
### Step 1: DR Initialization

#### 1.1 Configure the managed clusters
Apply the Secret, StorageClass, and VolumeReplicationClass on **each** managed
cluster before the workload is deployed.

**Volume Group:** The VG can be created in either of the following ways:

1. **Through the CSI Driver:** Create a `VolumeGroup` CR, and the CSI Driver creates
  the VG on the storage system.
2. **Manually:** Create the VG directly on the storage system.

In both cases, set the VG name in the StorageClass `volume_group` parameter.

**Scheduling interval:** The VolumeReplicationClass `schedulingInterval` must match
the DRPolicy `schedulingInterval`.

**Reclaim policy:** The StorageClass `reclaimPolicy` must be `Retain`. During
Failover and Relocate, Ramen removes the PVCs from the old primary cluster, and
`Retain` keeps the underlying volumes on the storage system so they stay part of
the replicated VG.

**Ramen labels:** The following labels link the StorageClass and
VolumeReplicationClass to Ramen:

| Label | Applied to | Description |
|---|---|---|
| `ramendr.openshift.io/storageid` | StorageClass, VolumeReplicationClass | Identifies the storage system. Same on both objects within a cluster, but **different** on each managed cluster, since each cluster uses its own storage system. |
| `ramendr.openshift.io/replicationid` | StorageClass, VolumeReplicationClass | Identifies the replication relationship. **Same** on both objects and on both managed clusters. |
| `ramendr.openshift.io/replication-class` | VolumeReplicationClass | Matched by the DRPolicy `replicationClassSelector` on the hub. |


**Managed Cluster 1**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <secret-name>
  namespace: <namespace>
type: Opaque
stringData:
  management_address: <storage-system-1-management-address>
  username: <username>
  password: <password>
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: <storage-class-name>
  labels:
    ramendr.openshift.io/replicationid: <replication-id>
    ramendr.openshift.io/storageid: <storage-id-1>
provisioner: block.csi.ibm.com
reclaimPolicy: Retain
allowVolumeExpansion: true
parameters:
  csi.storage.k8s.io/controller-publish-secret-name: <secret-name>
  csi.storage.k8s.io/controller-publish-secret-namespace: <namespace>
  csi.storage.k8s.io/provisioner-secret-name: <secret-name>
  csi.storage.k8s.io/provisioner-secret-namespace: <namespace>
  csi.storage.k8s.io/fstype: <fstype>
  pool: <pool-name>
  volume_group: <volume-group-name>
  volume_name_prefix: <volume-name-prefix>
---
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: <volume-replication-class-name>
  labels:
    ramendr.openshift.io/replication-class: <replication-class>
    ramendr.openshift.io/replicationid: <replication-id>
    ramendr.openshift.io/storageid: <storage-id-1>
spec:
  provisioner: block.csi.ibm.com
  parameters:
    replication.storage.openshift.io/replication-secret-name: <secret-name>
    replication.storage.openshift.io/replication-secret-namespace: <namespace>
    replication_policy: <replication-policy-name>
    schedulingInterval: <scheduling-interval>
```

**Managed Cluster 2**

Apply the same objects as Managed Cluster 1, with these differences:

- **Secret:** `management_address` is `<storage-system-2-management-address>`.
- **StorageClass and VolumeReplicationClass:** `ramendr.openshift.io/storageid` is
  `<storage-id-2>`.

#### 1.2 Configure the hub
Apply the DRPolicy and DRPC on the hub cluster.

- **DRPolicy:** Its `replicationClassSelector` matches the `replication-class`
  label on the VolumeReplicationClass, and its `schedulingInterval` matches the
  VolumeReplicationClass `schedulingInterval`.
- **DRPC:** Its `pvcSelector` matches the `appname` label on the PVCs.

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: <drpolicy-name>
spec:
  drClusters:
  - <managed-cluster-1>
  - <managed-cluster-2>
  replicationClassSelector:
    matchLabels:
      ramendr.openshift.io/replication-class: <replication-class>
  schedulingInterval: <scheduling-interval>
---
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: <drpc-name>
  namespace: <namespace>
spec:
  drPolicyRef:
    name: <drpolicy-name>
  placementRef:
    kind: Placement
    name: <placement-name>
  preferredCluster: <managed-cluster-1>
  pvcSelector:
    matchLabels:
      appname: <app-name>
```

#### 1.3 Label the workload PVCs and deploy the workload
Each PVC to protect must use the StorageClass from 1.1 and carry the label
selected by the DRPC `pvcSelector`. Include **all PVCs** that belong to the VG.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: <pvc-name>
  namespace: <namespace>
  labels:
    appname: <app-name>
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: <storage-class-name>
  resources:
    requests:
      storage: <size>
```

Deploy the workload to `<managed-cluster-1>` using the Placement referenced by the
DRPC.

**Workload deployment options:** Ramen supports several approaches for protecting
and replicating application resources across clusters, such as ACM Subscription
and Argo CD ApplicationSet. For more information, refer to the
[Ramen Workload Resource Protection documentation](https://github.com/RamenDR/ramen/blob/main/README.md#workload-resource-protection).

**Note:** The DRPC can also be applied after the workload is deployed, to add DR
protection to a workload that is already running.

#### 1.4 Verify DR initialization
Check each of the following, in order.

**a) DRPolicy peer classes (hub)**

Check that `status.async.peerClasses` is populated with the cluster IDs,
`replicationID`, `storageClassName`, and `storageID`. This confirms that Ramen has
discovered and paired the StorageClasses and VolumeReplicationClasses of both
managed clusters.

```bash
oc get drpolicy <drpolicy-name> -o jsonpath='{.status.async.peerClasses}'
```

**b) DRPC status (hub)**

Check that the DRPC phase is `Deployed` and the progression is `Completed`.

```bash
oc get drpc <drpc-name> -n <namespace> -o wide
```

**c) VolumeReplicationGroup (managed clusters)**

Check that Ramen has created a VRG on both managed clusters, with state
**Primary** on `<managed-cluster-1>` and **Secondary** on `<managed-cluster-2>`.

```bash
oc get vrg -n <namespace>
```

**d) VolumeReplication (primary managed cluster)**

Check that the workload is running on `<managed-cluster-1>`, and that a
`VolumeReplication` exists for each protected PVC and is in the **Primary** state.

```bash
oc get pod,pvc,volumereplication -n <namespace>
```

**e) Replication policy on the storage VG**

On the primary storage system, check that the replication policy is assigned to
the VG that the PVCs belong to.

---

### Step 2: Failover or Relocate
Both operations are started on the **hub**, either from the ACM console or by
patching the DRPC with the CLI.

#### 2.1 Failover (unplanned)
Moves the workload from `<managed-cluster-1>` to `<managed-cluster-2>` when
`<managed-cluster-1>` is unavailable. Ramen first **promotes** the VG on
`<managed-cluster-2>`, then cleans up the workload and **demotes** the VG on
`<managed-cluster-1>` when it is reachable.

- **ACM console:** Go to **Applications**, open the actions menu of the
  application, and select **Failover application**. Select `<managed-cluster-2>`
  as the target cluster and start the failover.
- **Hub CLI:**

```bash
oc patch drpc <drpc-name> -n <namespace> --type merge \
  -p '{"spec":{"action":"Failover","failoverCluster":"<managed-cluster-2>"}}'
```

#### 2.2 Verify Failover
Check each of the following, in order.

**a) Promote on the new primary (`<managed-cluster-2>`)**

Check that the workload is running, and that the VRG and the `VolumeReplication`
for each protected PVC are in the **Primary** state.

```bash
oc get pod,pvc,vrg,volumereplication -n <namespace>
```

**b) Cleanup and demote on the old primary (`<managed-cluster-1>`)**

While this step runs, the DRPC progression on the hub shows `Cleaning Up`. When it
completes, check that the workload and its resources are removed, and that only
the VRG remains, in the **Secondary** state.

```bash
oc get pod,pvc,vrg,volumereplication -n <namespace>
```

**c) DRPC status (hub)**

Check that the DRPC phase is `FailedOver` and the progression is `Completed`.

```bash
oc get drpc <drpc-name> -n <namespace> -o wide
```

**d) VG replication mode on storage**

Check that the VG is now **primary** on the storage system of `<managed-cluster-2>`
and **secondary** on the storage system of `<managed-cluster-1>`.

#### 2.3 Relocate (planned)
Moves the workload back from `<managed-cluster-2>` to `<managed-cluster-1>`. Ramen
first cleans up the workload and **demotes** the VG on `<managed-cluster-2>`, then
**promotes** the VG on `<managed-cluster-1>`.

- **ACM console:** Go to **Applications**, open the actions menu of the
  application, and select **Relocate application**. Select `<managed-cluster-1>`
  as the target cluster and start the relocation.
- **Hub CLI:**

```bash
oc patch drpc <drpc-name> -n <namespace> --type merge \
  -p '{"spec":{"action":"Relocate","preferredCluster":"<managed-cluster-1>"}}'
```

#### 2.4 Verify Relocate
Check each of the following, in order.

**a) Cleanup and demote on the current primary (`<managed-cluster-2>`)**

Check that the workload and its resources are removed, and that only the VRG
remains, in the **Secondary** state.

```bash
oc get pod,pvc,vrg,volumereplication -n <namespace>
```

**b) Promote on the new primary (`<managed-cluster-1>`)**

Check that the workload is running, and that the VRG and the `VolumeReplication`
for each protected PVC are in the **Primary** state.

```bash
oc get pod,pvc,vrg,volumereplication -n <namespace>
```

**c) DRPC status (hub)**

Check that the DRPC phase is `Relocated` and the progression is `Completed`.

```bash
oc get drpc <drpc-name> -n <namespace> -o wide
```

**d) VG replication roles on storage**

Check that the VG is now **primary** on the storage system of `<managed-cluster-1>`
and **secondary** on the storage system of `<managed-cluster-2>`.

---