# Creating a StatefulSet

Create a StatefulSet yaml file to manage stateful applications.

The IBM® block storage CSI driver supports using both file system and raw block volume types.

StatefulSets can include volumes with file systems, raw block volume systems, or both.

**Important:** When defining the StatefulSet configuration, be sure to define volumes according to the PVC type.

Use the sections below for yaml creation of StatefulSets with file system, raw block volume, and mixed types. After each yaml file creation, use the `kubectl apply` command.

```
kubectl apply -f <filename>.yaml
```

The `statefulset.apps/<filename> created` message is emitted.

## Creating a StatefulSet with file system volumes

Create a StatefulSet yaml file, similar to the following demo-statefulset-file-system.yaml file.

<pre>
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
        <b>volumeMounts:
          - name: demo-volume-file-system
            mountPath: "/data"</b>
      volumes:
      - name: demo-volume-file-system
        persistentVolumeClaim:
          claimName: demo-pvc-file-system
</pre>

## Creating a StatefulSet with raw block volume

Create a StatefulSet yaml file, similar to the following demo-statefulset-raw-block.yaml file.

<pre>
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
        <b>volumeDevices:
          - name: demo-volume-raw-block
            devicePath: "/dev/block"</b>
      volumes:
      - name: demo-volume-raw-block
        persistentVolumeClaim:
          claimName: demo-pvc-raw-block
</pre>

## Creating a StatefulSet with both raw block and file system volumes

Create a StatefulSet yaml file, similar to the following demo-statefulset-combined.yaml file.

<pre>
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
        <b>volumeMounts:
          - name: demo-volume-file-system
            mountPath: "/data"
        volumeDevices:
          - name: demo-volume-raw-block
            devicePath: "/dev/block"</b>            
      volumes:
      - name: demo-volume-file-system
        persistentVolumeClaim:
          claimName: demo-pvc-file-system
      - name: demo-volume-raw-block
        persistentVolumeClaim:
          claimName: demo-pvc-raw-block
</pre>


