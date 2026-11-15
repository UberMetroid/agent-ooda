package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSONMap(t *testing.T, p string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad JSON in %s: %v", p, err)
	}
	return m
}

func TestWirePreserveAndLegacyPy(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	setPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	stateDir := filepath.Join(tmp, "state")
	_ = os.MkdirAll(filepath.Dir(setPath), 0o755)
	_ = writeWireJSON(setPath, map[string]any{"theme": "dark"})

	var out, errB bytes.Buffer
	if rc := RunWireCmd([]string{"check", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("check failed rc=%d err=%q", rc, errB.String())
	}
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("wire failed rc=%d err=%q", rc, errB.String())
	}
	data := readJSONMap(t, setPath)
	if data["theme"] != "dark" {
		t.Fatalf("lost existing key: %+v", data)
	}
	sl := data["statusLine"].(map[string]any)
	if sl["command"] != "~/.gemini/antigravity-cli/statusline.py" {
		t.Fatalf("unexpected command: %+v", sl)
	}

	// User customizations stay intact across idempotent re-wire.
	sl["enabled"] = false
	sl["padding"] = 3
	_ = writeWireJSON(setPath, data)
	out.Reset()
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "already wired") {
		t.Fatalf("re-wire mismatch rc=%d out=%q", rc, out.String())
	}
	sl2 := readJSONMap(t, setPath)["statusLine"].(map[string]any)
	if sl2["enabled"] != false || sl2["padding"] != float64(3) {
		t.Fatalf("idempotent re-wire clobbered user settings: %+v", sl2)
	}

	// Legacy python command upgrades in place preserving padding without archiving.
	sl2["command"] = "python3 ~/.gemini/antigravity-cli/statusline.py"
	data["statusLine"] = sl2
	_ = writeWireJSON(setPath, data)
	out.Reset()
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("legacy-py upgrade failed rc=%d", rc)
	}
	sl3 := readJSONMap(t, setPath)["statusLine"].(map[string]any)
	if sl3["command"] != "~/.gemini/antigravity-cli/statusline.py" || sl3["padding"] != float64(3) {
		t.Fatalf("legacy-py lost custom fields: %+v", sl3)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, prevName)); !os.IsNotExist(err) {
		t.Fatalf("legacy-py upgrade must not archive prev")
	}
}

func TestWireForeignNullAndRestore(t *testing.T) {
	tmp := t.TempDir()
	setPath := filepath.Join(tmp, "settings.json")
	stateDir := filepath.Join(tmp, "state")
	_ = writeWireJSON(setPath, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "echo first & ok"},
	})
	var out, errB bytes.Buffer
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("wire foreign failed: %d (%q)", rc, errB.String())
	}
	rawSet, _ := os.ReadFile(setPath)
	rawPrev, _ := os.ReadFile(filepath.Join(stateDir, prevName))
	if !strings.Contains(string(rawPrev), "echo first & ok") {
		t.Fatalf("expected unescaped & in prev: %s", rawPrev)
	}

	// Overwrite with second foreign block and wire again -> archive first.
	_ = writeWireJSON(setPath, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "echo second"},
	})
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("second foreign wire failed: %d", rc)
	}
	archMatches, _ := filepath.Glob(filepath.Join(stateDir, "backups", "statusline-prev-*.json"))
	if len(archMatches) != 1 {
		t.Fatalf("expected archived prev in backups, got %v", archMatches)
	}
	if rc := RunWireCmd([]string{"unwire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("unwire failed: %d (%q)", rc, errB.String())
	}
	restored := readJSONMap(t, setPath)["statusLine"].(map[string]any)
	if restored["command"] != "echo second" {
		t.Fatalf("unwire restored wrong block: %+v", restored)
	}

	// Explicit null statusLine is saved and restored verbatim.
	_ = os.WriteFile(setPath, []byte(`{"statusLine": null}`), 0o644)
	_ = os.RemoveAll(filepath.Join(tmp, "state"))
	_ = rawSet
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("wire over null failed: %d", rc)
	}
	if rc := RunWireCmd([]string{"unwire", setPath, stateDir}, &out, &errB); rc != 0 {
		t.Fatalf("unwire null failed: %d", rc)
	}
	nullMap := readJSONMap(t, setPath)
	val, exists := nullMap["statusLine"]
	if !exists || val != nil {
		t.Fatalf("expected explicit null restored, got exists=%v val=%v", exists, val)
	}
}

func TestWireRefusesCorruptAndCleans(t *testing.T) {
	tmp := t.TempDir()
	setPath := filepath.Join(tmp, "settings.json")
	stateDir := filepath.Join(tmp, "state")
	_ = os.WriteFile(setPath, []byte("{bad json"), 0o644)
	var out, errB bytes.Buffer
	if rc := RunWireCmd([]string{"check", setPath, stateDir}, &out, &errB); rc != 1 {
		t.Fatalf("expected malformed settings exit 1, got %d", rc)
	}
	_ = os.WriteFile(setPath, []byte("[1, 2]"), 0o644)
	if rc := RunWireCmd([]string{"wire", setPath, stateDir}, &out, &errB); rc != 1 {
		t.Fatalf("expected array settings exit 1, got %d", rc)
	}
	_ = writeWireJSON(setPath, map[string]any{"ok": true})
	_ = os.MkdirAll(stateDir, 0o755)
	_ = os.WriteFile(filepath.Join(stateDir, prevName), []byte("{corrupt"), 0o644)
	if rc := RunWireCmd([]string{"check", setPath, stateDir}, &out, &errB); rc != 1 {
		t.Fatalf("expected corrupt prev check exit 1, got %d", rc)
	}
	tmps, _ := filepath.Glob(filepath.Join(tmp, "*tmp*"))
	if len(tmps) != 0 {
		t.Fatalf("leftover tmp files found: %v", tmps)
	}
}
