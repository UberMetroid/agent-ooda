package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	agyRelDir = "~/.gemini/antigravity-cli"
	prevName  = "statusline.prev.json"
)

func wireTargets(setPath string) (string, map[string]bool) {
	absSet, _ := filepath.Abs(setPath)
	sibling := filepath.Clean(filepath.Join(filepath.Dir(absSet), "statusline.py"))
	canonical := expandUserPath(agyRelDir + "/statusline.py")
	cmd := sibling
	if sibling == canonical {
		cmd = agyRelDir + "/statusline.py"
	}
	return cmd, map[string]bool{canonical: true, sibling: true}
}

func archivePrevStatus(stateDir string, cur any, outW io.Writer) error {
	prev := filepath.Join(stateDir, prevName)
	if _, herr := os.Lstat(prev); herr == nil {
		older, oerr := loadWireJSON(prev, false)
		if oerr != nil {
			return oerr
		}
		oldB, _ := json.Marshal(older)
		curB, _ := json.Marshal(cur)
		if bytes.Equal(oldB, curB) {
			return nil
		}
		bDir := filepath.Join(stateDir, "backups")
		if err := os.MkdirAll(bDir, 0o700); err != nil {
			return err
		}
		arch := filepath.Join(bDir, fmt.Sprintf("statusline-prev-%d.json", os.Getpid()))
		if err := writeWireJSON(arch, older); err != nil {
			return err
		}
		fmt.Fprintf(outW, "archived older statusLine to %s\n", arch)
	}
	if err := writeWireJSON(prev, cur); err != nil {
		return err
	}
	fmt.Fprintf(outW, "saved previous statusLine to %s\n", prev)
	return nil
}

func applyWire(setPath, stateDir, cmd string, owned map[string]bool, outW, errW io.Writer) int {
	raw, err := loadWireJSON(setPath, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	data := raw.(map[string]any)
	cur, hasKey := data["statusLine"]
	cls := classifyStatusCmd(cur, owned)
	if cls == "direct" {
		fmt.Fprintf(outW, "statusLine already wired in %s\n", setPath)
		return 0
	}
	if cls == "legacy-py" {
		if curM, ok := cur.(map[string]any); ok {
			curM["command"] = cmd
			if werr := writeWireJSON(setPath, data); werr != nil {
				fmt.Fprintln(errW, werr)
				return 1
			}
			fmt.Fprintf(outW, "statusLine wired in %s\n", setPath)
			return 0
		}
	}
	if hasKey && cls == "foreign" {
		if aerr := archivePrevStatus(stateDir, cur, outW); aerr != nil {
			fmt.Fprintln(errW, aerr)
			return 1
		}
	}
	data["statusLine"] = map[string]any{
		"type": "command", "command": cmd, "enabled": true, "padding": 0, "stack_with_default": true,
	}
	if werr := writeWireJSON(setPath, data); werr != nil {
		fmt.Fprintln(errW, werr)
		return 1
	}
	fmt.Fprintf(outW, "statusLine wired in %s\n", setPath)
	return 0
}

func applyUnwire(setPath, stateDir string, owned map[string]bool, outW, errW io.Writer) int {
	if _, err := os.Stat(setPath); os.IsNotExist(err) {
		fmt.Fprintf(outW, "%s not present; nothing to unwire\n", setPath)
		return 0
	}
	raw, err := loadWireJSON(setPath, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	data := raw.(map[string]any)
	if classifyStatusCmd(data["statusLine"], owned) == "foreign" {
		fmt.Fprintf(outW, "no agy-router statusLine block in %s; nothing to unwire\n", setPath)
		return 0
	}
	delete(data, "statusLine")
	prev := filepath.Join(stateDir, prevName)
	_, perr := os.Lstat(prev)
	if perr == nil {
		pv, lerr := loadWireJSON(prev, false)
		if lerr != nil {
			fmt.Fprintln(errW, lerr)
			return 1
		}
		data["statusLine"] = pv
	}
	if werr := writeWireJSON(setPath, data); werr != nil {
		fmt.Fprintln(errW, werr)
		return 1
	}
	fmt.Fprintf(outW, "statusLine removed from %s\n", setPath)
	if perr == nil {
		_ = os.Remove(prev)
		fmt.Fprintf(outW, "statusLine restored from %s\n", prev)
	}
	return 0
}

// RunWireCmd runs `check|wire|unwire <settings.json> [<state_dir>]`.
func RunWireCmd(argv []string, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	if len(argv) < 2 || (argv[0] != "check" && argv[0] != "wire" && argv[0] != "unwire") {
		fmt.Fprintln(errW, "usage: __wire check|wire|unwire <settings.json> [<state_dir>]")
		return 2
	}
	action, setPath := argv[0], expandUserPath(argv[1])
	cmd, owned := wireTargets(setPath)
	if action == "check" {
		if _, err := loadWireJSON(setPath, true); err != nil {
			fmt.Fprintln(errW, err)
			return 1
		}
		if err := checkWritablePath(setPath); err != nil {
			fmt.Fprintln(errW, err)
			return 1
		}
		if len(argv) > 2 {
			prev := filepath.Join(expandUserPath(argv[2]), prevName)
			if _, err := os.Lstat(prev); err == nil {
				if _, lerr := loadWireJSON(prev, false); lerr != nil {
					fmt.Fprintln(errW, lerr)
					return 1
				}
			}
		}
		return 0
	}
	if len(argv) < 3 {
		fmt.Fprintf(errW, "usage: __wire %s <settings.json> <state_dir>\n", action)
		return 2
	}
	stateDir := expandUserPath(argv[2])
	if action == "wire" {
		return applyWire(setPath, stateDir, cmd, owned, outW, errW)
	}
	return applyUnwire(setPath, stateDir, owned, outW, errW)
}
