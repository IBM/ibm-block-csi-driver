# Detached Installation of the Driver

The steps below can be applied for any detached Red Hat OpenShift container registry environment and on the amd64 architecture supported by Red Hat OpenShift and the IBM Block CSI Driver:

1. Create a custom operator index and mirror images
2. Create a custom catalog source and patch operator
3. Install the driver

## Create Custom Operator Index and Mirror Images

The IBM Block CSI driver is available from the Red Hat OpenShift Community Operator catalog. The catalog index is built by Red Hat and can be used a as a source for detecting and listing packages available in this operator catalog.<br>
The first step is to download the correct version of the oc-mirror tool for the correct CPU architecture.<br>
Identify the packages available in the Red Hat Community Operator catalog index using the following command. Examples below are shown for a Red Hat OpenShift 4.17 installation.<br>

```
$ oc-mirror list operators \
  --catalog=registry.redhat.io/redhat/community-operator-index:v4.17 \
  > community-operators.lst
```

Verify the IBM Block CSI Driver is listed in the catalog.

```
$ grep -i IBM community-operators.lst
…
ibm-block-csi-operator-community
…
```

Initialize the mirroring process.

```
$ oc-mirror init
kind: ImageSetConfiguration
apiVersion: mirror.openshift.io/v1alpha2
storageConfig:
  local:
    path: ./
mirror:
  platform:
    channels:
    - name: stable-4.17
      type: ocp
  operators:
  - catalog: registry.redhat.io/redhat/redhat-operator-index:v4.18
    packages:
    - name: serverless-operator
      channels:
      - name: stable
  additionalImages:
  - name: registry.redhat.io/ubi8/ubi:latest
  helm: {}
```

Copy the output above and modify it to obtain the following content.<br>
Save the content as ```ibm-block.yaml```. Update the Red Hat OpenShift catalog version in accordance with your installed version. For example:

```
$ cat block-csi.yaml
kind: ImageSetConfiguration
apiVersion: mirror.openshift.io/v1alpha2
storageConfig:
  local:
    path: ./archives
mirror:
#  platform:
#    channels:
#    - name: stable-4.17
#      type: ocp
  operators:
  - catalog: registry.redhat.io/redhat/community-operatorindex:v4.18
    packages:
    - name: ibm-block-csi-operator-community
      channels:
      - name: stable
#  additionalImages:
#  - name: registry.redhat.io/ubi8/ubi:latest
#  helm: {}
```

Initiate the catalog index creation.

```
$ oc-mirror --config ./ibm-block.yaml \
  docker://{your_private_registry_url} \
  --ignore-history [--dest-skip-tls]
```
The command above should generate the following files in your current working directory:

* ```CatalogSource``` manifest
* ```ImageContentSourcePolicy``` manifest

These two files can be used in the next section.<br>
The IBM Block CSI Driver itself uses the following container images:

* ```k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.9.0```
* ```k8s.gcr.io/sig-storage/csi-provisioner:v3.6.0```
* ```k8s.gcr.io/sig-storage/csi-attacher:v4.4.0```
* ```k8s.gcr.io/sig-storage/csi-snapshotter:v6.3. ```
* ```k8s.gcr.io/sig-storage/csi-resizer:v1.9.0```
* ```k8s.gcr.io/sig-storage/livenessprobe:v2.12.0```
* ```quay.io/ibmcsiblock/csi-block-volumereplication-operator:v0.9.0```
* ```quay.io/ibmcsiblock/csi-volume-group-operator:v0.9.1```
* ```quay.io/ibmcsiblock/ibm-block-csi-host-definer-amd64:1.12.0```
* ```quay.io/ibmcsiblock/ibm-block-csi-operator-amd64:1.12.0 ```
* ```quay.io/ibmcsiblock/ibm-block-csi-driver-controller-amd64:1.12.0```
* ```quay.io/ibmcsiblock/ibm-block-csi-driver-node-amd64:1.12.0```

All the images above must be copied to your private registry using ```skopeo```.<br>

**Note**: Make sure to run the ```skopeo``` command from a node that uses the same CPU architecture as the node where you intend to deploy the operator.

## Create CatalogSource and Patch Operator (CSV)

Once all the images have been copied to your private registry, the IBM Block CSI Driver CSV must be patched to remove all container image references using a tag.

1. Create your CatalogSource using the manifest from the previous section
2. Deploy the IBM Block CSI Driver Operator via the Red Hat OpenShift Console
3. Patch the IBM Block CSI Driver CSV, setting all image paths and tags to match your private registry images

**Note**: To edit the CSV, go to the Red Hat OpenShift console or use the ```oc edit``` command. Use ```skopeo inspect``` to identify the digest of each image.

## Install the Driver

Once the private registry is set up, the CatalogSource is configured and the IBM Block CSI Driver Operator is installed and patched, install the IBM Block CSI Driver

1. Download the driver installation yaml
```
curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.0/config/samples/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
```
2. Update the driver installation yaml, setting all image paths and tags to match your private registry images
3. Optionally, download the HostDefiner driver installation yaml and update it, setting all iamge paths and tags to match your private registry images
```
 curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.0/config/samples/csi_v1_hostdefiner_cr.yaml > csi_v1_hostdefiner_cr.yaml
 ```

