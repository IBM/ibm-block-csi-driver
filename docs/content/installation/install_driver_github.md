
{{site.data.keyword.attribute-definition-list}}

# Installing the driver with GitHub

The operator for IBM® block storage CSI driver can be installed directly with GitHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Use the following steps to install the operator and driver, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

Before you begin, it is best practice to create a user-defined namespace. Create the project namespace, using the `kubectl create ns <namespace>` command.{: tip}

When host definer is being installed, it is preferable to do so before installing the CSI driver IBMBlockCSI custom resource (see [Installing the host definer](install_hostdefiner.md)).{: tip}

1.  Install the operator.

    1. Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.1/deploy/installer/generated/ibm-block-csi-operator.yaml > ibm-block-csi-operator.yaml
        ```

    2.  (Optional) Update the image fields in the `ibm-block-csi-operator.yaml`.

If a user-defined namespace was created, edit the namespace from `default` to `<namespace>`.{: important}

    3. Install the operator.

        ```
        kubectl apply -f ibm-block-csi-operator.yaml
        ```

    4. Verify that the operator is running. (Make sure that the Status is _Running_.)

        ```
        $> kubectl get pod -l app.kubernetes.io/name=ibm-block-csi-operator -n <namespace>
        NAME                                    READY   STATUS    RESTARTS   AGE
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running   0          10m
        ```

2.  Install the IBM block storage CSI driver by creating an IBMBlockCSI custom resource.

    1.  Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.1/config/samples/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```

    2.  (Optional) Update the image repository field, tag field, or both in the `csi.ibm.com_v1_ibmblockcsi_cr.yaml`.

If a user-defined namespace was created, edit the namespace from `default` to `<namespace>`.{: important}

    3.  Install the `csi.ibm.com_v1_ibmblockcsi_cr.yaml`.

        ```
        kubectl apply -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```
    
    4.  Verify that the driver is running:
        ```
        $> kubectl get pods -n <namespace> -l product=ibm-block-csi-driver
        NAME                                    READY   STATUS  RESTARTS AGE
        ibm-block-csi-controller-0              7/7     Running 0        9m36s
        ibm-block-csi-node-jvmvh                3/3     Running 0        9m36s
        ibm-block-csi-node-tsppw                3/3     Running 0        9m36s
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running 0        10m
        ```


