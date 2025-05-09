FROM golang:1-alpine3.21 AS builder

RUN apk add --no-cache git ca-certificates build-base su-exec olm-dev

WORKDIR /build

# Copy and download dependency using go mod
COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN ./build.sh

FROM alpine:3.21

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache su-exec ca-certificates olm bash jq yq curl

COPY --from=builder /build/meshtastic-bridge /usr/bin/meshtastic-bridge
COPY --from=builder /build/docker-run.sh /docker-run.sh
VOLUME /data

CMD ["/docker-run.sh"]