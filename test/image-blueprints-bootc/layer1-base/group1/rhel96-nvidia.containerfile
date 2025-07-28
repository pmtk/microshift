#FROM registry.redhat.io/rhel9-eus/rhel-9.6-bootc:9.6
FROM registry.redhat.io/openshift4/microshift-bootc-rhel9:v4.19

# Override proxy settings from parent image
ENV NO_PROXY=''
ENV HTTP_PROXY=''
ENV HTTPS_PROXY=''

ARG ARCH=x86_64
ARG NVIDIA_DRIVER_VERSION=575.57.08

ARG DP_MANIFEST_VER="v0.17.2"
ARG DP_MANIFEST_DEST="/etc/microshift/manifests.d/10-nvidia-device-plugin"
ARG DP_MANIFEST_URL="https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/refs/tags/${DP_MANIFEST_VER}/deployments/static/nvidia-device-plugin-privileged-with-service-account.yml"

# Download and build the NVIDIA driver
# RUN curl \
#         -o /tmp/nvidia_driver-linux-x86_64-${NVIDIA_DRIVER_VERSION}-archive.tar.xz \
#         https://developer.download.nvidia.com/compute/nvidia-driver/redist/nvidia_driver/linux-x86_64/nvidia_driver-linux-x86_64-${NVIDIA_DRIVER_VERSION}-archive.tar.xz && \
#     tar xf /tmp/nvidia_driver-linux-x86_64-${NVIDIA_DRIVER_VERSION}-archive.tar.xz -C /tmp/ && \
#     KERNEL_VER=$(rpm -q --qf "%{VERSION}-%{RELEASE}" kernel) && \
#     KERNEL_VER_ARCH="${KERNEL_VER}.$(uname -m)" && \
#     dnf install -y g++ make "kernel-devel-${KERNEL_VER}" && \
#     dnf clean all && \
#     cd /tmp/nvidia_driver-linux-x86_64-${NVIDIA_DRIVER_VERSION}-archive/kernel-open && \
#     KERNEL_UNAME="${KERNEL_VER_ARCH}" make && \
#     mkdir -p /usr/lib/modules/${KERNEL_VER_ARCH}/extra && \
#     cp nvidia*.ko /usr/lib/modules/${KERNEL_VER_ARCH}/extra/ && \
#     echo 'blacklist nouveau' | tee /etc/modprobe.d/nouveau-blacklist.conf && \
#     depmod -a ${KERNEL_VER_ARCH} && \
#     rm -rf /tmp/nvidia_driver-linux-x86_64-${NVIDIA_DRIVER_VERSION}-archive/

RUN \
    curl \
      -o /tmp/nvidia-driver.run \
      https://us.download.nvidia.com/XFree86/Linux-${ARCH}/${NVIDIA_DRIVER_VERSION}/NVIDIA-Linux-${ARCH}-${NVIDIA_DRIVER_VERSION}.run  && \
    chmod +x /tmp/nvidia-driver.run && \
    KERNEL_VER=$(rpm -q --qf "%{VERSION}-%{RELEASE}" kernel) && \
    KERNEL_VER_ARCH="${KERNEL_VER}.${ARCH}" && \
    dnf install -y g++ make "kernel-devel-${KERNEL_VER}" && \
    dnf clean all && \
    /tmp/nvidia-driver.run --kernel-name=${KERNEL_VER_ARCH} --no-systemd --no-rebuild-initramfs --silent && \
    rm -rf /tmp/nvidia-driver.run

RUN \
    dnf config-manager --add-repo "https://developer.download.nvidia.com/compute/cuda/repos/rhel9/${ARCH}/cuda-rhel9.repo" && \
    dnf install -y nvidia-fabricmanager nvidia-persistenced && \
    systemctl enable nvidia-fabricmanager.service && \
    systemctl enable nvidia-persistenced.service && \
    dnf clean all

RUN \
    curl -s -L https://nvidia.github.io/libnvidia-container/stable/rpm/nvidia-container-toolkit.repo | tee /etc/yum.repos.d/nvidia-container-toolkit.repo && \
    dnf config-manager --enable nvidia-container-toolkit-experimental && \
    dnf install -y \
      nvidia-container-toolkit \
      nvidia-container-toolkit-base \
      libnvidia-container-tools \
      libnvidia-container1 && \
    dnf clean all && \
    setsebool -P container_use_devices on && \
    nvidia-ctk cdi generate --mode=csv --output=/etc/cdi/nvidia.yaml && \
    nvidia-ctk runtime configure --runtime=crio --set-as-default --config=/etc/crio/crio.conf.d/99-nvidia.conf && \
    sed -i 's/^runtimes =.*$/runtimes = ["crun", "docker-runc", "runc"]/g' /etc/nvidia-container-runtime/config.toml

RUN \
    mkdir -p "${DP_MANIFEST_DEST}" && \
    curl -s -L "${DP_MANIFEST_URL}" -o "${DP_MANIFEST_DEST}/nvidia-device-plugin.yml"

COPY <<EOF "${DP_MANIFEST_DEST}/kustomization.yaml"
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - nvidia-device-plugin.yml
EOF
