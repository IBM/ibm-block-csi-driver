# Creating a Secret

Create an array secret YAML file in order to define the storage credentials (username and password) and address.

**Important:** When your storage system password is changed, be sure to also change the passwords in the corresponding secrets, particularly when LDAP is used on the storage systems. <br /><br />Failing to do so causes mismatched passwords across the storage systems and the secrets, causing the user to be locked out of the storage systems.

**Note:** If using the CSI Topology feature, follow the steps in [Creating a Secret with topology awareness](creating_secret_topology_aware.md).

Use one of the following procedures to create and apply the secret:
  - [Creating an array secret file](#creating-an-array-secret-file)
  - [Creating an array secret via command line](#creating-an-array-secret-via-command-line)

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
    stringData:
      management_address: demo-management-address  # Array management addresses
      username: demo-username                      # Array username
    data:
      password: ZGVtby1wYXNzd29yZA==               # base64 array password
     ```
       
2. Apply the secret using the following command:

      ```
      kubectl apply -f <filename>.yaml
      ```

    The `secret/<secret-name> created` message is emitted.


## Creating an array secret via command line
**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create the secret using the following command:

```
kubectl create secret generic demo-secret --from-literal=username=demo-username --from-literal=password=demo-password --from-literal=management_address=demo-management-address -n default
```