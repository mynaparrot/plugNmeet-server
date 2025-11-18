FROM golang:1.25 AS builder

ARG TARGETPLATFORM
ARG TARGETARCH
RUN echo "Building for $TARGETPLATFORM"

WORKDIR /go/src/app

COPY main.go main.go
COPY go.mod go.mod
COPY go.sum go.sum
# download if above files changed
RUN go mod download

# Copy the go source
COPY helpers/ helpers/
COPY pkg/ pkg/
COPY version/ version/

# Define SPEECHSDK_ROOT for the builder stage
ENV SPEECHSDK_ROOT=/opt/speechsdk

 # Download and extract Speech SDK
RUN mkdir -p "$SPEECHSDK_ROOT" && \
     wget -O SpeechSDK-Linux.tar.gz https://aka.ms/csspeech/linuxbinary && \
     tar --strip 1 -xzf SpeechSDK-Linux.tar.gz -C "$SPEECHSDK_ROOT" && \
     rm SpeechSDK-Linux.tar.gz

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt update && \
    apt install --no-install-recommends -y build-essential ca-certificates \
    libasound2-dev libopus-dev libopusfile-dev libssl-dev libsoxr-dev  && \
    apt clean && \
    rm -rf /var/lib/apt/lists/*

# Build the Go application with CGO enabled and dynamic Speech SDK paths
RUN case "$TARGETARCH" in \
        "amd64") SPEECHSDK_ARCH_DIR="x64" ;; \
        "arm64") SPEECHSDK_ARCH_DIR="arm64" ;; \
        *) echo "Unsupported architecture for Speech SDK: $TARGETARCH"; exit 1 ;; \
    esac && \
    CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH GO111MODULE=on \
    CGO_CFLAGS="-I$SPEECHSDK_ROOT/include/c_api" \
    CGO_LDFLAGS="-L$SPEECHSDK_ROOT/lib/${SPEECHSDK_ARCH_DIR} -lMicrosoft.CognitiveServices.Speech.core" \
    go build -trimpath -ldflags '-w -s -buildid=' -a -o plugnmeet-server main.go

FROM debian:stable-slim

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt update && \
    apt install --no-install-recommends -y wget ca-certificates libreoffice mupdf-tools \
    libasound2 libssl3 libopus0 libsoxr0 libopusfile0 && \
    apt clean && \
    rm -rf /var/lib/apt/lists/*

# Copy the compiled application
COPY --from=builder /go/src/app/plugnmeet-server /usr/bin/plugnmeet-server

# Copy the Speech SDK libraries from the builder stage
COPY --from=builder /opt/speechsdk /opt/speechsdk

# Copy the entrypoint script
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Run the entrypoint script, which sets up the environment and runs the binary
ENTRYPOINT ["docker-entrypoint.sh"]
