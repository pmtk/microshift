# C2CC Manual Setup with COPR RPMs

Two RHEL 9.6 VMs running MicroShift with Cluster-to-Cluster Connectivity (c2cc).
VM1 uses default CIDRs (10.42/10.43), VM2 uses custom CIDRs (10.45/10.46).

## 0. Set VM IPs

On each VM, set both IPs so the subsequent commands can reference them:

```bash
export IP1=10.100.0.1
export IP2=10.100.0.2
```

## 1. Install MicroShift and configure firewall

On each VM install MicroShift and open firewall ports:

```bash
sudo dnf copr enable -y @microshift-io/experimental-c2cc
sudo dnf install -y microshift-io-dependencies
sudo dnf install -y microshift microshift-networking

sudo firewall-cmd --permanent --zone=trusted --add-source=10.42.0.0/16
sudo firewall-cmd --permanent --zone=trusted --add-source=10.43.0.0/16
sudo firewall-cmd --permanent --zone=trusted --add-source=10.45.0.0/16
sudo firewall-cmd --permanent --zone=trusted --add-source=10.46.0.0/16
sudo firewall-cmd --permanent --zone=trusted --add-source=169.254.169.1
sudo firewall-cmd --permanent --zone=public --add-port=6443/tcp
```

## 2. Configure MicroShift

### VM1

```bash
sudo mkdir -p /etc/microshift 
cat <<CFG | sudo -E tee /etc/microshift/config.yaml
telemetry:
    status: Disabled
c2cc:
    remoteClusters:
        - nextHop: "${IP2}"
          clusterNetwork: "10.45.0.0/16"
          serviceNetwork: "10.46.0.0/16"
CFG

sudo -E firewall-cmd --permanent --zone=trusted --add-source="${IP2}/32"
sudo firewall-cmd --reload
sudo systemctl enable --now microshift
```

### VM2

```bash
sudo mkdir -p /etc/microshift 
cat <<CFG | sudo -E tee /etc/microshift/config.yaml
telemetry:
    status: Disabled
network:
    clusterNetwork:
        - 10.45.0.0/16
    serviceNetwork:
        - 10.46.0.0/16
c2cc:
    remoteClusters:
        - nextHop: "${IP1}"
          clusterNetwork: "10.42.0.0/16"
          serviceNetwork: "10.43.0.0/16"
CFG

sudo -E firewall-cmd --permanent --zone=trusted --add-source="${IP1}/32"
sudo firewall-cmd --reload
sudo systemctl enable --now microshift
```

## 3. Set up kubeconfig

On each VM:

```bash
mkdir -p ~/.kube
sudo cat /var/lib/microshift/resources/kubeadmin/kubeconfig > ~/.kube/config
```

## 4. Verify

On each VM, wait for MicroShift to be ready:

```bash
oc get pods -A
```

Check c2cc routes exist (it can take up to 15s *after* the ovn-kubernetes Pods start):

On VM1: should see routes to 10.45.0.0/24 and 10.46.0.0/16
```bash
ip route | grep -E '10\.4[56]\.'
```

On VM2: should see routes to 10.42.0.0/24 and 10.43.0.0/16
```bash
ip route | grep -E '10\.4[23]\.'
```

## 5. Test Cross-Cluster Connectivity

On each VM, deploy test Pods.

```bash
oc create namespace c2cc
oc apply -n c2cc -f - <<'YAML'
apiVersion: v1
kind: Pod
metadata:
  name: hello-microshift
  labels:
    app: hello-microshift
spec:
  terminationGracePeriodSeconds: 0
  containers:
  - name: hello-microshift
    image: quay.io/microshift/busybox:1.36
    command: ["/bin/sh"]
    args:
    - -c
    - |
      MSG="Hello from ${MY_POD_IP}"
      LEN=$(( ${#MSG} + 1 ))
      while true; do
        echo -ne "HTTP/1.0 200 OK\r\nContent-Length: ${LEN}\r\n\r\n${MSG}\n" | nc -l -p 8080
      done
    ports:
    - containerPort: 8080
    env:
    - name: MY_POD_IP
      valueFrom:
        fieldRef:
          fieldPath: status.podIP
    securityContext:
      allowPrivilegeEscalation: false
      capabilities: { drop: [ALL] }
      runAsNonRoot: true
      runAsUser: 1001
      runAsGroup: 1001
      seccompProfile: { type: RuntimeDefault }
---
apiVersion: v1
kind: Service
metadata:
  name: hello-microshift
  labels:
    app: hello-microshift
spec:
  selector:
    app: hello-microshift
  ports:
  - port: 8080
    targetPort: 8080
---
apiVersion: v1
kind: Pod
metadata:
  name: network-debug-pod
spec:
  securityContext:
    runAsNonRoot: true
    seccompProfile: { type: RuntimeDefault }
  containers:
  - name: debug-container
    image: docker.io/nicolaka/netshoot:latest
    command: ["/bin/sh", "-c", "sleep infinity"]
    securityContext:
      allowPrivilegeEscalation: false
      capabilities: { drop: [ALL] }
      runAsUser: 10001
YAML
```

On each VM, wait for pods to be ready:

```bash
oc wait -n c2cc pod/hello-microshift pod/network-debug-pod --for=condition=Ready --timeout=120s
```

Get the remote cluster's target IPs using following command and saving the values of IP and CLUSTER-IP columns:

```bash
oc get pods,svc -n c2cc -o wide
```

Then run connectivity tests:

```bash
REMOTE_POD_IP=<hello-microshift pod IP from another MicroShift cluster>
REMOTE_SVC_IP=<hello-microshift service IP from another MicroShift cluster>

oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_POD_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_SVC_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 http://hello-microshift.c2cc.svc.other-cluster.local:8080
```

Each response shows which cluster answered by its pod IP, e.g. `Hello from 10.42.0.7`.

Example:
```bash
# VM1
$ oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_POD_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_SVC_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 http://hello-microshift.c2cc.svc.other-cluster.local:8080

Hello from 10.45.0.7
Hello from 10.45.0.7
Hello from 10.45.0.7
```

```bash
# VM2
$ oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_POD_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 "http://${REMOTE_SVC_IP}:8080"
oc exec network-debug-pod -n c2cc -- curl -sS --max-time 10 http://hello-microshift.c2cc.svc.other-cluster.local:8080
Hello from 10.42.0.7
Hello from 10.42.0.7
Hello from 10.42.0.7
```
