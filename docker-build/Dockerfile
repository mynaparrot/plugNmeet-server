# Start from our pre-built base image, which contains all heavy dependencies.
FROM mynaparrot/plugnmeet-server:base AS builder

ARG TARGETPLATFORM
ARG TARGETARCH
RUN echo "Building for $TARGETPLATFORM"

WORKDIR /go/src/app

# Copy Go module files and download dependencies.
# This is a separate layer to leverage caching if only Go code changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code.
COPY main.go ./
COPY pkg/ pkg/
COPY version/ version/

# Compile the Go application.
# CGO flags are still required to link against the pre-built libraries in the base image.
RUN case "$TARGETARCH" in \
        "amd64") SPEECHSDK_ARCH_DIR="x64" ;; \
        "arm64") SPEECHSDK_ARCH_DIR="arm64" ;; \
        *) echo "Unsupported architecture for Speech SDK: $TARGETARCH"; exit 1 ;; \
    esac && \
    CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH GO111MODULE=on \
    CGO_CFLAGS="-I$SPEECHSDK_ROOT/include/c_api" \
    CGO_LDFLAGS="-L$SPEECHSDK_ROOT/lib/${SPEECHSDK_ARCH_DIR} -lMicrosoft.CognitiveServices.Speech.core" \
    go build -trimpath -ldflags '-w -s -buildid=' -a -o plugnmeet-server main.go

# -------------------------
# Final, lean image
# -------------------------
FROM debian:stable-slim

# Install only the runtime dependencies.
RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update && \
    apt-get install --no-install-recommends -y \
    wget curl ca-certificates libreoffice \
    libasound2 libssl3 libopus0 libsoxr0 libopusfile0 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Copy the compiled binary and required assets from the builder stage.
COPY --from=builder /go/src/app/plugnmeet-server /usr/bin/plugnmeet-server
COPY --from=builder /usr/local/bin/mutool /usr/bin/mutool
COPY --from=builder /opt/speechsdk /opt/speechsdk

# Copy and set up the entrypoint.
COPY docker-build/docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["plugnmeet-server"]
