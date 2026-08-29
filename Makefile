VERSION ?= 0.1.0
BINARY  ?= licode
LDFLAGS  = -s -w -X main.version=$(VERSION)
DIST     = build
UPX      ?= upx

.PHONY: build build-linux build-darwin build-windows cross upx size install clean test

# 与 build.sh 一致的输出目录 build/，先测试再编译
build:
	go test ./...
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe .
	@echo "构建完成，见 $(DIST)/"

# 47 平台一键脚本（原脚本，输出 build/）
build-all:
	./build.sh

# 当前主机版本
build-host:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) .

# 安装到 bin：/usr/local/bin 优先，不可写则 ~/.local/bin（已在 PATH）
PREFIX ?= /usr/local
install: build-host
	@if [ -w /usr/local/bin ]; then \
		install -m 0755 $(DIST)/$(BINARY) /usr/local/bin/$(BINARY); \
		echo "已安装到 /usr/local/bin/$(BINARY)"; \
	else \
		mkdir -p $$HOME/.local/bin; \
		install -m 0755 $(DIST)/$(BINARY) $$HOME/.local/bin/$(BINARY); \
		echo "已安装到 $$HOME/.local/bin/$(BINARY)（/usr/local/bin 需 root）"; \
	fi
	@echo "直接运行 $(BINARY) 进入 TUI"

# 可选：UPX 压缩
upx:
	@command -v $(UPX) >/dev/null 2>&1 || { echo "未找到 upx，跳过压缩"; exit 0; }
	@for f in $(DIST)/$(BINARY)*; do $(UPX) --best --lzma "$$f" 2>/dev/null || true; done

size:
	@ls -lh $(DIST)/ 2>/dev/null || echo "先运行 make build 或 ./build.sh"

clean:
	rm -rf $(DIST)

test:
	go test ./...