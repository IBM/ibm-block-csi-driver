
{{site.data.keyword.attribute-definition-list}}

# Creating a Secret

Create an array secret YAML file in order to define the storage credentials (username and password) and address.

When your storage system password is changed, be sure to also change the passwords in the corresponding secrets, particularly when LDAP is used on the storage systems. Failing to do so causes mismatched passwords across the storage systems and the secrets, causing the user to be locked out of the storage systems.{: important}

If using the CSI Topology feature, follow the steps in [Creating a Secret with topology awareness](creating_secret_topology_aware.md).{: attention}

Use one of the following procedures to create and apply the secret:

## Creating an array secret file
1. Create the secret file, similar to the following `demo-secret.yaml`:

    The `management_address` field can contain more than one address, with each value separated by a comma.

    ```
    kind: Secret
    apiVersion: v1
    metadata:
      name:  demo-secret
      namespace: default
    type: Opaque
    data:
      management_address: demo-management-address-base64  # Array management IP addresses
      username: demo-username-base64                      # Array username
      port_set: PortSet-base64                            # (Optional) Per-secret port set, for new ports only
                                                          # Overrides global port-set config
      password: ZGVtby1wYXNzd29yZA==                      # base64 array password
     ```

    The way to achieve this, for example, is:
    ```
    $ echo -n '172.17.210.1'| base64
    MTcyLjE3LjIxMC4x
    $ echo -n 'password'| base64
    cGFzc3dvcmQ=
    $ echo -n 'username'| base64
    dXNlcm5hbWU=
    ```
    and then use it in the yaml:
    ```
    kind: Secret
    apiVersion: v1
    metadata:
      name: secret
      namespace: default
    data:
      management_address: MTcyLjE3LjIxMC4x
      password: cGFzc3dvcmQ=
      username: dXNlcm5hbWU=
    type: Opaque
    ```
       
2. Apply the secret using the following command:

      ```
      kubectl apply -f <filename>.yaml
      ```

    The `secret/<secret-name> created` message is emitted.


## Creating an array secret for use in IBM Storage Virtualize® partitions

To use IBM Storage Virtualize® partitions - two additional parameters need to be specified:
partition_name - name of partition on storage
partition_default_vg - name of volume group associated with the partition to be used when new volumes are created

IBM CSI doesn't configure the partition or volume group - this is configured by the user directly on the storage.

A secret similar to `demo-secret.yaml` but for a partitions setup:


    ```
    kind: Secret
    apiVersion: v1
    metadata:
      name:  demo-secret
      namespace: default
    type: Opaque
    stringData:
      management_address: demo-management-address  # Array management addresses
      username: demo-username                      # Array username
      partition_name: SVCPartition                 # Partition name as defined on storage
      partition_default_vg: PartitionDefaultVG     # Volume group as defined on storage, part of the partition
    data:
      password: ZGVtby1wYXNzd29yZA==               # base64 array password
     ```


## Creating an array secret via command line

This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.{: tip}

Create the secret using the following command:

```
kubectl create secret generic demo-secret --from-literal=username=demo-username --from-literal=password=demo-password --from-literal=management_address=demo-management-address -n default
```
