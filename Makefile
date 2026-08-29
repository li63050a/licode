VERSION ?= 0.1.0
BINARY  ?= licode
LDFLAGS  = -s -w -X main.version=$(VERSION)
DIST     = dist
UPX      ?= upx

.PHONY: build build-linux build-darwin build-windows cross upx size clean test

build: build-linux build-darwin build-windows
	@echo "全部构建完成，见 $(DIST)/"

build-linux:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 .

build-darwin:
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 .

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe .

# 当前主机版本（直接 ./dist/licode 运行）
build-host:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) .

cross: build

# 可选：UPX 进一步压缩
upx:
	@command -v $(UPX) >/dev/null 2>&1 || { echo "未找到 upx，跳过压缩"; exit 0; }
	@for f in $(DIST)/$(BINARY)*; do $(UPX) --best --lzma "$$f" 2>/dev/null || true; done

size:
	@ls -lh $(DIST)/ 2>/dev/null || echo "先运行 make build"

clean:
	rm -rf $(DIST)

test:
	go test ./...