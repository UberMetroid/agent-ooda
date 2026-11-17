GO ?= go
AGY ?= agy

.PHONY: all build portable line-cap density vocab shape plugin-check test dogfood install uninstall

all: build portable line-cap density vocab shape plugin-check test dogfood

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o plugins/agy-router/bin/agy-router ./cmd/agy-router
	ln -sfn agy-router plugins/agy-router/bin/agy-router-hud
	ln -sfn agy-router plugins/agy-router/bin/agy-router-hook

portable:
	$(GO) run ./qa/gates --gate portable

line-cap:
	$(GO) run ./qa/gates --gate line-cap

density:
	$(GO) run ./qa/gates --gate density

vocab:
	$(GO) run ./qa/gates --gate vocab

shape:
	$(GO) run ./qa/gates --gate shape

plugin-check: build
	$(GO) run ./qa/gates --gate plugin-check --agy $(AGY)

test: build
	$(GO) vet ./... && $(GO) test -race -count=1 ./...

dogfood: build
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	export AGY_ROUTER_QUOTA_FILE="$$tmp/quota.json" AGY_ROUTER_DISCOVERY_FILE="$$tmp/discovery.json" \
	  AGY_ROUTER_NO_AUTO_PROBE=1 AGY_ROUTER_HUD_DIR="$$tmp/hud" AGY_ROUTER_DEPTH=0 AGENT_OODA_DEPTH=0 ROUTER_DEPTH=0; \
	ver=$$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' plugins/agy-router/plugin.json); \
	./plugins/agy-router/bin/agy-router --version | grep -q "agy-router v$$ver"; \
	./plugins/agy-router/bin/agy-router --status >/dev/null; \
	./plugins/agy-router/bin/agy-router --discover >/dev/null; \
	./plugins/agy-router/bin/agy-router --usage >/dev/null; \
	test "$$(printf '{"terminal_width":120}\n' | NO_COLOR=1 ./plugins/agy-router/bin/agy-router-hud)" = "ROUTER"; \
	printf '{"terminal_width":120,"agent_state":"working","cycle_mode":"plan"}\n' | NO_COLOR=1 ./plugins/agy-router/bin/agy-router-hud | grep -q "◈ orient"; \
	printf '{"conversationId":"dogfood","hookEventName":"SessionStart"}\n' | ./plugins/agy-router/bin/agy-router-hook >/dev/null; \
	test -f "$$tmp/hud/current_session.json"; \
	out=$$(AGY_ROUTER_DEPTH=0 AGENT_OODA_DEPTH=0 ROUTER_DEPTH=1 ./plugins/agy-router/bin/agy-router --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive agy-router dispatch blocked"; \
	out=$$(AGY_ROUTER_DEPTH=1 AGENT_OODA_DEPTH=0 ROUTER_DEPTH=0 ./plugins/agy-router/bin/agy-router --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive agy-router dispatch blocked"; \
	echo "dogfood: ok (cli version/status/usage + hud + hook + recursion guard)"

# Not part of `all`: these mutate $HOME (symlinks, settings.json statusLine).
install:
	./install.sh

uninstall:
	./install.sh --uninstall
