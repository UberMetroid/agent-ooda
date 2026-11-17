GO ?= go
AGY ?= agy

.PHONY: all build portable line-cap density vocab shape plugin-check test dogfood install uninstall

all: build portable line-cap density vocab shape plugin-check test dogfood

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o plugins/agent-ooda/bin/agent-ooda ./cmd/agent-ooda
	ln -sfn agent-ooda plugins/agent-ooda/bin/agent-ooda-hud
	ln -sfn agent-ooda plugins/agent-ooda/bin/agent-ooda-hook

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
	export AGENT_OODA_QUOTA_FILE="$$tmp/quota.json" AGENT_OODA_DISCOVERY_FILE="$$tmp/discovery.json" \
	  OODA_NO_AUTO_PROBE=1 AGENT_OODA_HUD_DIR="$$tmp/hud" AGENT_OODA_DEPTH=0 ROUTER_DEPTH=0; \
	ver=$$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' plugins/agent-ooda/plugin.json); \
	./plugins/agent-ooda/bin/agent-ooda --version | grep -q "agent-ooda v$$ver"; \
	./plugins/agent-ooda/bin/agent-ooda --status >/dev/null; \
	./plugins/agent-ooda/bin/agent-ooda --discover >/dev/null; \
	./plugins/agent-ooda/bin/agent-ooda --usage >/dev/null; \
	test -z "$$(printf '{"terminal_width":120}\n' | NO_COLOR=1 ./plugins/agent-ooda/bin/agent-ooda-hud)"; \
	printf '{"terminal_width":120,"agent_state":"working","cycle_mode":"plan"}\n' | NO_COLOR=1 ./plugins/agent-ooda/bin/agent-ooda-hud | grep -q "orient"; \
	printf '{"conversationId":"dogfood","hookEventName":"SessionStart"}\n' | ./plugins/agent-ooda/bin/agent-ooda-hook >/dev/null; \
	test -f "$$tmp/hud/current_session.json"; \
	out=$$(AGENT_OODA_DEPTH=0 ROUTER_DEPTH=1 ./plugins/agent-ooda/bin/agent-ooda --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive agent-ooda dispatch blocked"; \
	out=$$(AGENT_OODA_DEPTH=1 ROUTER_DEPTH=0 ./plugins/agent-ooda/bin/agent-ooda --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive agent-ooda dispatch blocked"; \
	echo "dogfood: ok (cli version/status/usage + hud + hook + recursion guard)"

# Not part of `all`: these mutate $HOME (symlinks, settings.json statusLine).
install:
	./install.sh

uninstall:
	./install.sh --uninstall
