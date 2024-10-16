#!/bin/bash
set -euo pipefail

SCRIPTDIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
# shellcheck source=test/bin/common.sh
source "${SCRIPTDIR}/common.sh"

DISTRIBUTION_VERSION=2.8.3
REGISTRY_IMAGE="quay.io/microshift/distribution:${DISTRIBUTION_VERSION}"
REGISTRY_HOST=${REGISTRY_HOST:-$(hostname):5000}
PULL_SECRET=${PULL_SECRET:-${HOME}/.pull-secret.json}
LOCAL_REGISTRY_NAME="microshift-local-registry"

retry_pull_image() {
    for attempt in $(seq 3) ; do
        if ! podman pull "$@" ; then
            echo "WARNING: Failed to pull image, retry #${attempt}"
        else
            return 0
        fi
        sleep 10
    done

    echo "ERROR: Failed to pull image, quitting after 3 tries"
    return 1
}

prereqs() {
    "${SCRIPTDIR}/../../scripts/dnf_retry.sh" "install" "podman skopeo jq"
    podman stop "${LOCAL_REGISTRY_NAME}" || true
    podman rm "${LOCAL_REGISTRY_NAME}" || true
    retry_pull_image "${REGISTRY_IMAGE}"
    podman run -d -p 5000:5000 --restart always --name "${LOCAL_REGISTRY_NAME}" "${REGISTRY_IMAGE}"
}

setup_registry() {
    # Docker distribution does not support TLS authentication. The mirror-images.sh helper uses skopeo without tls options
    # and it defaults to https. Since this is not supported we need to configure registries.conf so that skopeo tries http instead.
    sudo bash -c 'cat > /etc/containers/registries.conf.d/900-microshift-mirror.conf' << EOF
[[registry]]
location = "$(hostname)"
insecure = true
EOF
    sudo systemctl restart podman
}

mirror_images() {
    local -r ifile=$1
    local -r ofile=$(mktemp /tmp/container-list.XXXXXXXX)

    sort -u "${ifile}" > "${ofile}"
    "${ROOTDIR}/scripts/image-builder/mirror-images.sh" --mirror "${PULL_SECRET}" "${ofile}" "${REGISTRY_HOST}"
    rm -f "${ofile}"
}

usage() {
    echo ""
    echo "Usage: ${0} [-f PATH]"
    echo "   -f PATH    File containing the containers to mirror. Defaults to ${CONTAINER_LIST}"
    exit 1
}

LIST_FILE="${CONTAINER_LIST}"

while [ $# -gt 0 ]; do
    case $1 in
    -f)
        shift
        LIST_FILE=$1
        ;;
    *)
        usage
        ;;
    esac
    shift
done

if [ ! -f "${LIST_FILE}" ]; then
    echo "File ${LIST_FILE} does not exist"
    exit 1
fi

prereqs
setup_registry
mirror_images "${LIST_FILE}"
