
{{site.data.keyword.attribute-definition-list}}

# Creating a StatefulSet

Create a StatefulSet YAML file to manage stateful applications.

The IBM® block storage CSI driver supports both file system and raw block volume modes.

StatefulSets can include file system volumes, raw block volumes, or both.

When defining the StatefulSet configuration, be sure to define volumes according to the PVC volume mode.{: important}

Use the following sections for YAML creation of StatefulSets with file system, raw block volume, and mixed volume modes.

  - [Creating a StatefulSet with file system volumes](#creating-a-statefulset-with-file-system-volumes)
  - [Creating a StatefulSet with raw block volume](#creating-a-statefulset-with-raw-block-volume)
  - [Creating a StatefulSet with both raw block and file system volumes](#creating-a-statefulset-with-both-raw-block-and-file-system-volumes)


After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `statefulset.apps/<statefulset-name> created` message is emitted.

## Creating a StatefulSet with file system volumes

Create a StatefulSet YAML file, similar to the following `demo-statefulset-file-system.yaml` file.

Be sure to indicate the `volumeMounts`, listing each volume's name and path. In this example, the `mountPath` is listed as `"/data"`.

    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: demo-statefulset-file-system
    spec:
      selector:
        matchLabels:
          app: demo-statefulset
      serviceName: demo-statefulset
      replicas: 1
      template:
        metadata:
          labels:
            app: demo-statefulset
        spec:
          containers:
          - name: demo-container
            image: registry.access.redhat.com/ubi8/ubi:latest
            command: [ "/bin/sh", "-c", "--" ]
            args: [ "while true; do sleep 30; done;" ]
            volumeMounts:
              - name: demo-volume-file-system
                mountPath: "/data"
          volumes:
          - name: demo-volume-file-system
            persistentVolumeClaim:
              claimName: demo-pvc-file-system

## Creating a StatefulSet with raw block volume

Create a StatefulSet YAML file, similar to the following `demo-statefulset-raw-block.yaml` file.

Be sure to indicate the `volumeDevices`, listing each volume's name and path. In this example, the `devicePath` is listed as `"/dev/block"`.

    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: demo-statefulset-raw-block
    spec:
      selector:
        matchLabels:
          app: demo-statefulset
      serviceName: demo-statefulset
      replicas: 1
      template:
        metadata:
          labels:
            app: demo-statefulset
        spec:
          containers:
          - name: demo-container
            image: registry.access.redhat.com/ubi8/ubi:latest
            command: [ "/bin/sh", "-c", "--" ]
            args: [ "while true; do sleep 30; done;" ]
            volumeDevices:
              - name: demo-volume-raw-block
                devicePath: "/dev/block"
          volumes:
          - name: demo-volume-raw-block
            persistentVolumeClaim:
              claimName: demo-pvc-raw-block

## Creating a StatefulSet with both raw block and file system volumes

Create a StatefulSet YAML file, similar to the following `demo-statefulset.yaml` file.

In a StatefulSet file that uses both volume modes, it is important to indicate both the `volumeMounts` and `volumeDevices` parameters.

    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: demo-statefulset
    spec:
      selector:
        matchLabels:
          app: demo-statefulset
      serviceName: demo-statefulset
      replicas: 1
      template:
        metadata:
          labels:
            app: demo-statefulset
        spec:
          containers:
          - name: demo-container
            image: registry.access.redhat.com/ubi8/ubi:latest
            command: [ "/bin/sh", "-c", "--" ]
            args: [ "while true; do sleep 30; done;" ]
            volumeMounts:
              - name: demo-volume-file-system
                mountPath: "/data"
            volumeDevices:
              - name: demo-volume-raw-block
                devicePath: "/dev/block"            
          volumes:
          - name: demo-volume-file-system
            persistentVolumeClaim:
              claimName: demo-pvc-file-system
          - name: demo-volume-raw-block
            persistentVolumeClaim:
              claimName: demo-pvc-raw-block

