#!/bin/sh

# This script sets the correct Speech SDK library path based on the
# architecture, updates the system's linker cache, and then executes
# the command passed to it.

set -e

# Determine architecture at runtime inside the container.
ARCH=$(dpkg --print-architecture)

case "$ARCH" in
    "amd64") SPEECHSDK_ARCH_DIR="x64" ;;
    "arm64") SPEECHSDK_ARCH_DIR="arm64" ;;
    *) echo "FATAL: Unsupported architecture for Speech SDK: $ARCH"; exit 1 ;;
esac

# Add the correct library path to a temporary ld.so.conf.d file
echo "/opt/speechsdk/lib/${SPEECHSDK_ARCH_DIR}" > /etc/ld.so.conf.d/speechsdk.conf

# Update the linker cache
ldconfig

# Execute the command passed as arguments to the script.
exec "$@"
