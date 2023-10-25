#!/usr/bin/env bash
# Usage: ./yq.sh
#   Installs the yq binary in ../bin directory

SCRIPT_DIR="$( dirname -- "${BASH_SOURCE[0]}"; )"
BIN_DIR="$( realpath "$SCRIPT_DIR/../bin" )"
mkdir -p "$BIN_DIR"

where="$BIN_DIR/yq"
YQ_VERSION=${YQ_VERSION:-v4.34.1}
hostArch=$(uname -m)
hostArch=${hostArch/x86_64/amd64}
YQ_BINARY=${YQ_BINARY:-yq_linux_$hostArch}

wget -q "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/${YQ_BINARY}" -O "$where"
chmod +x "${where}"
