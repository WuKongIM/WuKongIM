FROM golang:1.18 as build

ENV GOPROXY https://goproxy.cn,direct
ENV GO111MODULE on


WORKDIR /go/cache

ADD go.mod .
ADD go.sum .
RUN go mod download

WORKDIR /go/release

ADD . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -extldflags "-static"' -o app main.go

FROM alpine as prod
# Import the user and group files from the builder.
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
WORKDIR /home
COPY --from=build /go/release/app /home
ENTRYPOINT ["/home/app"]
