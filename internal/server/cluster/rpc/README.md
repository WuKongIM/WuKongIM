
下载protoc放在GOBIN目录下 （需要将GOBIN设置为PATH里）
https://github.com/protocolbuffers/protobuf/releases/download/v3.18.1/protoc-3.18.1-osx-x86_64.zip

// 安装protoc-gen-go
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest


protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ./internal/server/cluster/rpc/service.proto
