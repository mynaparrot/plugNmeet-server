FROM golang:1.22 as builder

ARG TARGETPLATFORM
ARG TARGETARCH
RUN echo building for "$TARGETPLATFORM"

WORKDIR /go/src/app

COPY go.mod go.mod
COPY go.sum go.sum
# download if above files changed
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY version/ version/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags '-w -s -buildid=' -a -o plugnmeet-server main.go

FROM debian:stable-slim

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt update && \
    apt install --no-install-recommends -y wget libreoffice mupdf-tools && \
    apt clean && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /go/src/app/plugnmeet-server /usr/bin/plugnmeet-server

# Run the binary.
ENTRYPOINT ["plugnmeet-server"]
