ARG GO_IMAGE=golang:1.25.0
ARG RUNTIME_IMAGE=alpine:3.19

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wukongim ./cmd/wukongim \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o /out/wkbench ./cmd/wkbench

FROM ${RUNTIME_IMAGE}
WORKDIR /app
COPY --from=builder /out/wukongim /usr/local/bin/wukongim
COPY --from=builder /out/wkbench /usr/local/bin/wkbench

EXPOSE 5001 5100 5200 7000
ENTRYPOINT ["/usr/local/bin/wukongim", "-config", "/etc/wukongim/wukongim.toml"]
