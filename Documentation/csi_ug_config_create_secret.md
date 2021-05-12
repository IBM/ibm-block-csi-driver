# Creating a Secret

Create an array secret YAML file in order to define the storage credentials \(username and password\) and address.

**Important:** When your storage system password is changed, be sure to also change the passwords in the corresponding secrets, particularly when LDAP is used on the storage systems.<br />Failing to do so causes mismatched passwords across the storage systems and the secrets, causingthe user to be locked out of the storage systems.

Use one of the following procedures to create and apply the secret:

## Creating an array secret file
1. Create the secret file, similar to the following demo-secret.yaml:

    The `management_address` field can contain more than one address, with each value separated by a comma.

    {% deploy kubernetes.examples.demo-secret %}

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

    `kubectl apply -f demo-secret.yaml`
    

     The `secret/<NAME> created` message is emitted.


## Creating an array secret via command line
**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create the secret using the following command:

 ```
 kubectl create secret generic <NAME> --from-literal=username=<USER> --from-literal=password=<PASSWORD>--from-literal=management_address=<ARRAY_MGMT>  -n <namespace\>
 ```
 
