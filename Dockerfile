FROM golang:1.20 as build

ENV GOPROXY https://goproxy.cn,direct
ENV GO111MODULE on

# 安装 Node.js 和 Yarn

RUN curl -sS https://dl.yarnpkg.com/debian/pubkey.gpg |  apt-key add -
RUN echo "deb https://dl.yarnpkg.com/debian/ stable main" |  tee /etc/apt/sources.list.d/yarn.list
RUN apt-get update -y  && apt-get install -y yarn
RUN apt-get install -y nodejs
RUN yarn config set registry https://registry.npm.taobao.org -g

# 编译前端
WORKDIR /go/release/web
ADD web .


RUN yarn install && yarn build

# 编译后端

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
COPY --from=build /go/release/web/dist /home/web/dist
ENTRYPOINT ["/home/app"]
