ARG GO_IMAGE=golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
ARG RUNTIME_IMAGE=alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG GOPROXY=https://goproxy.cn,direct

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS prometheus-source
ARG GOPROXY
ENV GOPROXY=${GOPROXY}
WORKDIR /prometheus
COPY docker/prometheus/toolchain.env /tmp/prometheus-toolchain.env
RUN set -eu; \
    . /tmp/prometheus-toolchain.env; \
    curl --fail --location --retry 3 --connect-timeout 15 --max-time 300 \
      --proto '=https' --tlsv1.2 \
      "https://codeload.github.com/prometheus/prometheus/tar.gz/${PROMETHEUS_SOURCE_COMMIT}" \
      -o /tmp/prometheus-source.tar.gz; \
    echo "$PROMETHEUS_SOURCE_SHA256  /tmp/prometheus-source.tar.gz" | sha256sum --check -; \
    tar -xzf /tmp/prometheus-source.tar.gz --strip-components=1 -C /prometheus; \
    GOWORK=off GOTOOLCHAIN=local go get \
      "golang.org/x/crypto@$PROMETHEUS_X_CRYPTO_VERSION" \
      "google.golang.org/grpc@$PROMETHEUS_GRPC_VERSION"; \
    GOWORK=off GOTOOLCHAIN=local go mod verify

FROM prometheus-source AS prometheus
ARG TARGETOS
ARG TARGETARCH
RUN set -eu; \
    . /tmp/prometheus-toolchain.env; \
    case "$TARGETOS/$TARGETARCH" in \
      linux/amd64|linux/arm64) ;; \
      *) echo "unsupported Prometheus platform: $TARGETOS/$TARGETARCH" >&2; exit 1 ;; \
    esac; \
    CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -mod=readonly -trimpath -buildvcs=false \
      -ldflags "-s -w -X github.com/prometheus/common/version.Version=$PROMETHEUS_VERSION -X github.com/prometheus/common/version.Revision=$PROMETHEUS_SOURCE_COMMIT" \
      -o "/out/bin/prometheus-linux-$TARGETARCH" ./cmd/prometheus; \
    install -D -m 0644 LICENSE /out/licenses/LICENSE; \
    install -D -m 0644 NOTICE /out/licenses/NOTICE

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG GOPROXY
ENV GOPROXY=${GOPROXY}
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=prometheus /out/bin/ ./internal/app/prometheus_embedded/
# Source builds retain development defaults; release builds supply immutable identity.
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
ARG BUILD_SOURCE=source
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -ldflags "-X main.buildVersion=$BUILD_VERSION -X main.buildCommit=$BUILD_COMMIT -X main.buildSource=$BUILD_SOURCE" -o /out/wukongim ./cmd/wukongim \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -ldflags "-X main.buildVersion=$BUILD_VERSION -X main.buildCommit=$BUILD_COMMIT -X main.buildSource=$BUILD_SOURCE" -o /out/wkcli ./cmd/wkcli \
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
COPY --from=builder --chown=root:root --chmod=0755 /out/wkcli /usr/local/bin/wkcli
COPY --from=builder --chown=root:root --chmod=0755 /out/wkanalysis /usr/local/bin/wkanalysis
COPY --from=builder --chown=root:root --chmod=0755 /out/wkcloudsim /usr/local/bin/wkcloudsim
COPY --from=prometheus --chown=root:root /out/licenses/ /usr/share/licenses/prometheus/

EXPOSE 5001 5100 5200 5301 7000 19092
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12 \
  CMD wget -q --spider -T 5 http://127.0.0.1:5001/readyz || exit 1
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/wukongim", "-config", "/etc/wukongim/wukongim.toml"]
