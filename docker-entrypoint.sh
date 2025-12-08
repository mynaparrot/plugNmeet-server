#!/bin/sh

# This script sets the correct Speech SDK library path based on the
# architecture and then executes the main application.

set -e

# Determine architecture at runtime inside the container.
ARCH=$(dpkg --print-architecture)

case "$ARCH" in
    "amd64") SPEECHSDK_ARCH_DIR="x64" ;;
    "arm64") SPEECHSDK_ARCH_DIR="arm64" ;;
    *) echo "FATAL: Unsupported architecture for Speech SDK: $ARCH"; exit 1 ;;
esac

export LD_LIBRARY_PATH="/opt/speechsdk/lib/${SPEECHSDK_ARCH_DIR}${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"

exec plugnmeet-server "$@"
