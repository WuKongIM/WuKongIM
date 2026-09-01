ARG GO_IMAGE=golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
ARG RUNTIME_IMAGE=alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG GOPROXY=https://goproxy.cn,direct

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG GOPROXY
ENV GOPROXY=${GOPROXY}
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wukongim ./cmd/wukongim \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wkbench ./cmd/wkbench \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wkanalysis ./cmd/wkanalysis \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wkcloudsim ./cmd/wkcloudsim

FROM ${RUNTIME_IMAGE}
RUN apk upgrade --no-cache \
 && addgroup -S -g 10001 wukongim \
 && adduser -S -D -H -u 10001 -G wukongim -h /var/lib/wukongim -s /sbin/nologin wukongim \
 && install -d -o wukongim -g wukongim -m 0750 /var/lib/wukongim /var/lib/wkbench /run/wukongim \
 && install -d -o root -g wukongim -m 0750 /etc/wukongim
WORKDIR /app
COPY --from=builder --chown=root:root --chmod=0755 /out/wukongim /usr/local/bin/wukongim
COPY --from=builder --chown=root:root --chmod=0755 /out/wkbench /usr/local/bin/wkbench
COPY --from=builder --chown=root:root --chmod=0755 /out/wkanalysis /usr/local/bin/wkanalysis
COPY --from=builder --chown=root:root --chmod=0755 /out/wkcloudsim /usr/local/bin/wkcloudsim

EXPOSE 5001 5100 5200 5301 7000 19092
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12 \
  CMD wget -q --spider -T 5 http://127.0.0.1:5001/readyz || exit 1
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/wukongim", "-config", "/etc/wukongim/wukongim.toml"]
