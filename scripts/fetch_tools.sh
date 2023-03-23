#!/usr/bin/env bash

set -euo pipefail
IFS=$'\n\t'

ARCH="$(uname -p)"
DEST_DIR="${DEST_DIR:-_output/bin}"

_download() {
    local output="$1"
    local url="$2"
    for _ in $(seq 1 5); do
        if curl -sSfLO --output-dir "${output}" "${url}"; then
            return 0
        fi
        sleep 5s
    done
    return 1
}

_install() {
    local url="$1"
    local checksum="$2"
    local filename="$3"
    local initial_filename="$4"
    local dest="$DEST_DIR/$filename"

    [[ -e "${dest}" ]] && return 0
    echo "Installing $filename to $DEST_DIR"

    tmp=$(mktemp -d)
    trap 'rm -rfv ${tmp} &>/dev/null' EXIT

    filename="$(basename "$url")"
    echo -n "$checksum -" >"$tmp/checksum.txt"

    _download "$tmp" "$url" 

    if ! sha256sum -c "${tmp}/checksum.txt" < "$tmp/$filename" &>/dev/null; then
        echo "  Checksum for $filename doesn't match"
        echo "    Expected: $checksum"
        echo "         Got: $(sha256sum < "$tmp/$filename" | cut -d' ' -f1)"
        return 1
    fi

    if [[ "$(file --brief --mime-type "$tmp/$filename")" != "application/x-executable" ]]; then
        (cd "$tmp" && tar xvf "$filename" --transform 's,.*\/,,g' --wildcards "*/$initial_filename" >/dev/null)
    fi

    chmod +x "$tmp/$initial_filename"
    mkdir -p "$(dirname "$dest")"
    mv "$tmp/$initial_filename" "$dest"
}

get_golangci-lint() {
    local ver="1.52.1"
    declare -A checksums=(
        ["x86_64"]="f31a6dc278aff92843acdc2671f17c753c6e2cb374d573c336479e92daed161f" 
        ["aarch64"]="30dbea4ddde140010981b491740b4dd9ba973ce53a1a2f447a5d57053efe51cf")

    declare -A arch_map=(
        ["x86_64"]="amd64" 
        ["aarch64"]="arm64")

    local arch="${arch_map[$ARCH]}"
    local checksum="${checksums[$ARCH]}"
    local filename="golangci-lint"

    local url="https://github.com/golangci/golangci-lint/releases/download/v${ver}/golangci-lint-${ver}-linux-${arch}.tar.gz"

    _install "$url" "$checksum" "$filename" "$filename"
}

get_shellcheck() {
    local ver="v0.9.0"
    declare -A checksums=(
        ["x86_64"]="700324c6dd0ebea0117591c6cc9d7350d9c7c5c287acbad7630fa17b1d4d9e2f" 
        ["aarch64"]="179c579ef3481317d130adebede74a34dbbc2df961a70916dd4039ebf0735fae")

    declare -A arch_map=(
        ["x86_64"]="x86_64"
        ["aarch64"]="aarch64")

    local arch="${arch_map[$ARCH]}"
    local checksum="${checksums[$ARCH]}"
    local filename="shellcheck"
    local url="https://github.com/koalaman/shellcheck/releases/download/$ver/shellcheck-$ver.linux.$arch.tar.xz"

    _install "$url" "$checksum" "$filename" "$filename"
}

get_kuttl() {
    local ver="0.15.0"
    declare -A checksums=(
        ["x86_64"]="f6edcf22e238fc71b5aa389ade37a9efce596017c90f6994141c45215ba0f862" 
        ["aarch64"]="a3393f2824e632a9aa0f17fdd5c763f9b633f7a7d3f58696e94885c6b3b8af96")

    declare -A arch_map=(
        ["x86_64"]="x86_64" 
        ["aarch64"]="arm64")

    local arch="${arch_map[$ARCH]}"
    local checksum="${checksums[$ARCH]}"
    local filename="kuttl"
    local url="https://github.com/kudobuilder/kuttl/releases/download/v${ver}/kubectl-kuttl_${ver}_linux_${arch}"

    _install "$url" "$checksum" "$filename" "kubectl-kuttl_${ver}_linux_${arch}"
}

get_yq() {
    local ver="4.26.1"
    declare -A checksums=(
        ["x86_64"]="4d3afe5ddf170ac7e70f4c23eea2969eca357947b56d5d96b8516bdf9ce56577" 
        ["aarch64"]="837a659c5a04599f3ee7300b85bf6ccabdfd7ce39f5222de27281e0ea5bcc477")

    declare -A arch_map=(
        ["x86_64"]="amd64" 
        ["aarch64"]="arm64")

    local arch="${arch_map[$ARCH]}"
    local checksum="${checksums[$ARCH]}"
    local filename="yq"
    local url="https://github.com/mikefarah/yq/releases/download/v${ver}/yq_linux_${arch}.tar.gz"

    _install "$url" "$checksum" "$filename" "yq_linux_${arch}"
}

[[ "$(uname -o)" == "GNU/Linux" ]] || { echo "Script only runs on Linux"; exit 1; }
[[ "$(uname -p)" =~ x86_64|aarch64 ]] || { echo "Only x86_64 and aarch64 architectures are supported"; exit 1; }

if [ $# -eq 1 ]; then
    "get_$1"
else
    get_golangci-lint
    get_shellcheck
    get_kuttl
    get_yq
fi
