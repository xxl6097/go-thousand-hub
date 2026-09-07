GO      ?= go
BIN     := dist
LDFLAGS := -s -w

.PHONY: all server agent linux dev test clean

all: server agent linux

server:
	#$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-server ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-server-linux-amd64 ./cmd/server
	#CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-server-linux-arm64 ./cmd/server

agent:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-agent-darwin ./cmd/agent

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-agent-linux-arm64 ./cmd/agent

# 本地开发:前端指向磁盘目录,改 webroot 即时生效
dev: server
	RC_WEB_DIR=internal/server/webroot RC_LISTEN=:8080 ./$(BIN)/rc-server

test:
	$(GO) vet ./...
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-server ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/rc-agent-linux-amd64 ./cmd/agent

clean:
	rm -rf $(BIN)
