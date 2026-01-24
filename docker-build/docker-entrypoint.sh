#!/bin/sh
set -e

# 1. Determine architecture at runtime
ARCH=$(dpkg --print-architecture)
case "$ARCH" in
    "amd64") SPEECHSDK_ARCH_DIR="x64" ;;
    "arm64") SPEECHSDK_ARCH_DIR="arm64" ;;
    *)
        echo "FATAL: Unsupported architecture for Speech SDK: $ARCH"
        exit 1
        ;;
esac

# 2. Configure the system's runtime linker cache for the application
echo "/opt/speechsdk/lib/${SPEECHSDK_ARCH_DIR}" > /etc/ld.so.conf.d/speechsdk.conf
ldconfig

# 3. Export CGO flags for build tools like 'air'
export CGO_CFLAGS="-I/opt/speechsdk/include/c_api"
export CGO_LDFLAGS="-L/opt/speechsdk/lib/${SPEECHSDK_ARCH_DIR} -lMicrosoft.CognitiveServices.Speech.core"

# 4. Change to the application directory
cd /app

# 5. Execute the main command (e.g., "plugnmeet-server" or "air")
exec "$@"
