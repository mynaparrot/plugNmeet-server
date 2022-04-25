FROM golang:1.18-alpine as builder

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
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags '-w -s -buildid=' -a -o plugnmeet-server ./cmd/server

FROM alpine

RUN apk add --no-cache libreoffice mupdf-tools

COPY --from=builder /go/src/app/plugnmeet-server /usr/bin/plugnmeet-server

# Run the binary.
ENTRYPOINT ["plugnmeet-server"]
