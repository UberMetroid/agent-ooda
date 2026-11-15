PYTHON ?= python3
AGY ?= agy

.PHONY: all portable line-cap density vocab shape plugin-check test dogfood

all: portable line-cap density vocab shape plugin-check test dogfood

portable:
	$(PYTHON) -m qa.gates.run_gates --gate portable

line-cap:
	$(PYTHON) -m qa.gates.run_gates --gate line-cap

density:
	$(PYTHON) -m qa.gates.run_gates --gate density

vocab:
	$(PYTHON) -m qa.gates.run_gates --gate vocab

shape:
	$(PYTHON) -m qa.gates.run_gates --gate shape

plugin-check:
	$(PYTHON) -c 'import json,subprocess,sys; p=subprocess.run([sys.argv[1],"plugin","validate","plugins/ooda","--strict","--json"],capture_output=True,text=True); d=json.loads(p.stdout or "{}"); f=d.get("findings") or []; print(f"plugin-check: ok ({len(f)} findings)" if p.returncode==0 and not f else p.stdout); sys.exit(0 if p.returncode==0 and not f else 1)' $(AGY)

test:
	PYTHONPATH=plugins/ooda/src:. $(PYTHON) -m unittest discover -s qa/tests -v

dogfood:
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	export AGENT_QUOTA_FILE="$$tmp/quota.json" ROUTER_HUD_DIR="$$tmp/hud" ROUTER_DEPTH=0; \
	./plugins/ooda/bin/ooda --status >/dev/null; \
	./plugins/ooda/bin/ooda --usage >/dev/null; \
	printf '{"terminal_width":120}\n' | NO_COLOR=1 ./plugins/ooda/bin/ooda-hud | grep -q "ooda"; \
	printf '{"conversationId":"dogfood","hookEventName":"SessionStart"}\n' | ./plugins/ooda/bin/ooda-hook >/dev/null; \
	test -f "$$tmp/hud/current_session.json"; \
	out=$$(ROUTER_DEPTH=1 ./plugins/ooda/bin/ooda --recon "probe" 2>&1 || echo "rc=$$?"); \
	printf '%s\n' "$$out" | grep -q "ROUTER_DEPTH"; \
	echo "dogfood: ok (cli status/usage + hud + hook + recursion guard)"
