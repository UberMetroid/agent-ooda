package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func loadWireJSON(path string, isSettings bool) (any, error) {
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("error: %s is not a regular file; refusing to overwrite", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("error: %s is not accessible: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && isSettings {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("error: malformed JSON in %s; refusing to overwrite: %v", path, err)
	}
	if isSettings && len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, fmt.Errorf("error: malformed JSON in %s; refusing to overwrite: %v", path, err)
	}
	if !isSettings {
		return val, nil
	}
	m, ok := val.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("error: %s must contain a JSON object; refusing to overwrite", path)
	}
	return m, nil
}

func writeWireJSON(path string, data any) error {
	parent := filepath.Dir(path)
	if abs, err := filepath.Abs(path); err == nil {
		parent = filepath.Dir(abs)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(buf.Bytes())
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func expandUserPath(raw string) string {
	s := os.ExpandEnv(strings.TrimSpace(raw))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if s == "~" {
			s = home
		} else if strings.HasPrefix(s, "~/") {
			s = filepath.Join(home, s[2:])
		}
	}
	return filepath.Clean(s)
}

func evalSymlinksBest(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	var tail []string
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return filepath.Clean(real)
		}
	}
	return abs
}

func checkWritablePath(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		abs = filepath.Clean(target)
	}
	if os.Geteuid() != 0 {
		if _, serr := os.Stat(abs); serr == nil && syscall.Access(abs, 2) != nil {
			return fmt.Errorf("error: %s is not writable", target)
		}
	}
	probe := filepath.Dir(abs)
	for {
		if st, serr := os.Stat(probe); serr == nil && st.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	if os.Geteuid() != 0 && syscall.Access(probe, 3) != nil {
		return fmt.Errorf("error: directory %s is not writable; cannot update %s", probe, target)
	}
	return nil
}

func shellSplit(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote byte
	hasTok, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			cur.WriteByte(c)
			esc, hasTok = false, true
			continue
		}
		if c == '\\' && quote != '\'' {
			esc = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
				hasTok = true
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote, hasTok = c, true
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			if hasTok {
				toks = append(toks, cur.String())
				cur.Reset()
				hasTok = false
			}
			continue
		}
		cur.WriteByte(c)
		hasTok = true
	}
	if hasTok {
		toks = append(toks, cur.String())
	}
	return toks
}

func classifyStatusCmd(block any, owned map[string]bool) string {
	m, ok := block.(map[string]any)
	if !ok {
		return "foreign"
	}
	cmdStr, ok := m["command"].(string)
	if !ok || strings.TrimSpace(cmdStr) == "" || strings.ContainsAny(cmdStr, ";|&><`\n") {
		return "foreign"
	}
	toks := shellSplit(cmdStr)
	if len(toks) == 0 {
		return "foreign"
	}
	if len(toks) == 1 && owned[expandUserPath(toks[0])] {
		return "direct"
	}
	if !strings.HasPrefix(filepath.Base(toks[0]), "python") {
		return "foreign"
	}
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		if t == "-c" || t == "-m" || t == "-" {
			return "foreign"
		}
		if t == "-W" || t == "-X" {
			i++
			continue
		}
		if !strings.HasPrefix(t, "-") {
			if i == len(toks)-1 && owned[expandUserPath(t)] {
				return "legacy-py"
			}
			return "foreign"
		}
	}
	return "foreign"
}
