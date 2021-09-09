# Creating a StatefulSet

Create a StatefulSet YAML file to manage stateful applications.

The IBM® block storage CSI driver supports using both file system and raw block volume types.

StatefulSets can include volumes with file systems, raw block volume systems, or both.

**Important:** When defining the StatefulSet configuration, be sure to define volumes according to the PVC type.

Use the sections below for YAML creation of StatefulSets with file system, raw block volume, and mixed types. After each YAML file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `statefulset.apps/<statefulset-name> created` message is emitted.

## Creating a StatefulSet with file system volumes

Create a StatefulSet YAML file, similar to the following `demo-statefulset-file-system.yaml` file.

Here, the `volumeMounts` indicates both the name of the volume, with the necessary `mountPath` of `"/data"`.

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
                mountPath: "/data"</b>
          volumes:
          - name: demo-volume-file-system
            persistentVolumeClaim:
              claimName: demo-pvc-file-system

## Creating a StatefulSet with raw block volume

Create a StatefulSet YAML file, similar to the following `demo-statefulset-raw-block.yaml` file.

Here, the `volumeDevices` indicates both the name of the volume, with the necessary `devicePath` of `"/dev/block"`.

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

Create a StatefulSet YAML file, similar to the following `demo-statefulset-combined.yaml` file.

In a mixed file, it is important to indicate both the `volumeMounts` and  `volumeDevices` parameters, where `mountPath` is `"/data"` and `devicePath` is `"/dev/block"`.

    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: demo-statefulset-combined
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


