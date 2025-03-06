#!/usr/bin/env bash

set -xeuo pipefail

### DRIVER

# Based on
# https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/index.html#red-hat-enterprise-linux

if ! lspci | grep -i nvidia; then
    echo "NVIDIA GPU was not found in the output of lspci"
    exit 1
fi

# baseos and appstream are already enabled by configure-vm.sh
sudo subscription-manager repos \
    --enable=codeready-builder-for-rhel-9-x86_64-rpms

sudo dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-9.noarch.rpm
sudo dnf config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/rhel9/x86_64/cuda-rhel9.repo

sudo dnf install g++ -y

sudo dnf module install nvidia-driver:open-dkms -y
sudo systemctl enable nvidia-persistenced

# Build and install driver for the kernel that will run after reboot
sudo dkms autoinstall -k "$(sudo grubby --default-kernel | sed 's,/boot/vmlinuz-,,g')"
