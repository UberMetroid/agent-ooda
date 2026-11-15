GO ?= go
AGY ?= agy

.PHONY: all build portable line-cap density vocab shape plugin-check test dogfood install uninstall

all: build portable line-cap density vocab shape plugin-check test dogfood

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o plugins/ooda/bin/ooda ./cmd/ooda
	ln -sfn ooda plugins/ooda/bin/ooda-hud
	ln -sfn ooda plugins/ooda/bin/ooda-hook

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
	export AGENT_QUOTA_FILE="$$tmp/quota.json" OODA_DISCOVERY_FILE="$$tmp/discovery.json" OODA_NO_AUTO_PROBE=1 ROUTER_HUD_DIR="$$tmp/hud" ROUTER_DEPTH=0; \
	./plugins/ooda/bin/ooda --status >/dev/null; \
	./plugins/ooda/bin/ooda --discover >/dev/null; \
	./plugins/ooda/bin/ooda --usage >/dev/null; \
	printf '{"terminal_width":120}\n' | NO_COLOR=1 ./plugins/ooda/bin/ooda-hud | grep -q "ooda"; \
	printf '{"conversationId":"dogfood","hookEventName":"SessionStart"}\n' | ./plugins/ooda/bin/ooda-hook >/dev/null; \
	test -f "$$tmp/hud/current_session.json"; \
	out=$$(ROUTER_DEPTH=1 ./plugins/ooda/bin/ooda --recon "probe" 2>&1 || echo "rc=$$?"); \
	printf '%s\n' "$$out" | grep -q "ROUTER_DEPTH"; \
	echo "dogfood: ok (cli status/usage + hud + hook + recursion guard)"

# Not part of `all`: these mutate $HOME (symlinks, settings.json statusLine).
install:
	./install.sh

uninstall:
	./install.sh --uninstall
