.PHONY: all build webui webui-dev webui-install test lint clean

# The agent binary. webui/dist is committed, so this needs no Node.
all: build

build:
	go build .

test:
	go test ./...

lint:
	go vet ./...
	gofmt -l . | grep -v '^webui/' || true

# Rebuild the control center. Run this after changing anything under webui/src
# and commit the result — webui/dist is embedded with go:embed.
webui: webui-install
	cd webui && npm run build

webui-install:
	cd webui && npm install --no-audit --no-fund

# Vite dev server with hot reload, proxying to an agent already running on
# :9470. Override with VITE_AGENT=https://localhost:9480 make webui-dev
webui-dev: webui-install
	cd webui && npm run dev

clean:
	rm -f davi-nfc-agent
	rm -rf webui/node_modules
