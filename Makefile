.PHONY: all build build-nowebui webui webui-dev webui-install test test-nowebui lint clean

# The agent binary. webui/frontend/dist is committed, so this needs no Node.
all: build

build:
	go build ./cmd/davi-nfc-agent

# Without the control center: no /control routes, no privileged API, and no
# embedded console. The agent's own protocol is unchanged.
build-nowebui:
	go build -tags nowebui ./cmd/davi-nfc-agent

test:
	go test ./...

test-nowebui:
	go test -tags nowebui ./...

lint:
	go vet ./...
	go vet -tags nowebui ./...
	gofmt -l . | grep -v '^webui/frontend/' || true

# Rebuild the control center. Run this after changing anything under
# webui/frontend/src and commit the result — dist is embedded with go:embed.
webui: webui-install
	cd webui/frontend && npm run build

webui-install:
	cd webui/frontend && npm install --no-audit --no-fund

# Vite dev server with hot reload, proxying to an agent already running on
# :9470. Override with VITE_AGENT=https://localhost:9480 make webui-dev
webui-dev: webui-install
	cd webui/frontend && npm run dev

clean:
	rm -f davi-nfc-agent
	rm -rf webui/frontend/node_modules
