
{{site.data.keyword.attribute-definition-list}}

# Installing the driver with the OpenShift web console

When using the Red Hat OpenShift Container Platform®, the operator for IBM® block storage CSI driver can be installed directly from Red Hat OpenShift Container Platform web console, through the OperatorHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Installation via the Red Hat OpenShift Container Platform® web console is available for the x86 platform with the certified and community operator versions, but for the IBM Z® and IBM Power Systems® platforms installation is only available with the community operator version.{: attention}

The Red Hat OpenShift Container Platform® uses the following `SecurityContextConstraints` for the following `serviceAccounts`:

This data is for informational purposes only. {: note}

|serviceAccount|SecurityContextConstraint|
|--------------|-------------------------|
|ibm-block-csi-operator|restricted|
|ibm-block-csi-controller-sa|anyuid|
|ibm-block-csi-node-sa|privileged|

1. From the web console, create a new project (also referred to as namespace).

2. Find and install **IBM® block storage CSI driver operator** within the new project.<br /><br />The **Operator Installation** form appears.

3. Set the **Installation Mode** to the project namespace selected previously.

4. Set the **Approval Strategy** to either **Automatic** or **Manual** as per your preference.

The general recommendation is to select **Automatic** option.{: tip}

5. Click **Install**.

6. Check the status of the IBM® block storage CSI driver operator.

    Wait until the **Status** is _Up to date_ and then _Succeeded_.

While waiting for the **Status** to change from _Up to date_ to _Succeeded_, you can check the pod progress and readiness status from **Workloads** > **Pods**.{: tip}

7. After the operator installation progress is complete, click the installed IBM® block storage CSI driver operator.

8. (Optional) Create the host definer (`HostDefiner`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed. For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

9. Create the IBM® block storage CSI driver (`IBMBlockCSI`).

    A YAML file opens in the web console. This file can be left as-is, or edited as needed.

10. Update the YAML file to include your user-defined namespace.

11. After everything is created, wait until the **Status** is _Running_.

12. (Optional) If planning on using volume snapshots (IBM FlashCopy® function), enable support on your Kubernetes cluster.

   For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.20: Kubernetes Volume Snapshot Moves to GA](https://kubernetes.io/blog/2020/12/10/kubernetes-1.20-volume-snapshot-moves-to-ga/).

   Install both the Snapshot CRDs and the Common Snapshot Controller once per cluster.

   The instructions and relevant YAML files to enable volume snapshots can be found at: [https://github.com/kubernetes-csi/external-snapshotter#usage](https://github.com/kubernetes-csi/external-snapshotter#usage)

13. (Optional) If planning on using policy-based replication with volume groups, enable support on your orchestration platform cluster and storage system.

    1. To enable support on your Kubernetes cluster, install the following replication CRDs once per cluster.

        ```
        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroupclasses.yaml
        oc apply -f csi.ibm.com_volumegroupclasses.yaml

        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroupcontents.yaml
        oc apply -f csi.ibm.com_volumegroupcontents.yaml

        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroups.yaml
        oc apply -f csi.ibm.com_volumegroups.yaml
        ```

    2. Enable policy-based replication on volume groups, see the following section within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/): **Administering** > **Managing policy-based replication** > **Assigning replication policies to volume groups**.

14. (Optional) If planning on using volume replication (remote copy function), enable support on your orchestration platform cluster and storage system.

    1. To enable support on your Kubernetes cluster, install the following volume group CRDs once per cluster.

        ```
        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.3.0/config/crd/bases/replication.storage.openshift.io_volumereplicationclasses.yaml
        oc apply -f ./replication.storage.openshift.io_volumereplicationclasses.yaml

        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.3.0/config/crd/bases/replication.storage.openshift.io_volumereplications.yaml
        oc apply -f ./replication.storage.openshift.io_volumereplications.yaml
        ```

    2. To enable support on your storage system, see the following section within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/en/): **Administering** > **Managing Copy Services** > **Managing remote-copy partnerships**.
