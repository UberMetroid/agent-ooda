package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var safeIDSubRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// PluginRootPath resolves the plugins/ooda directory dynamically at call time.
func PluginRootPath() string {
	if env := os.Getenv("OODA_PLUGIN_ROOT"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		if realExe, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = realExe
		}
		cand := filepath.Dir(filepath.Dir(exe))
		if st, serr := os.Stat(filepath.Join(cand, "plugin.json")); serr == nil && !st.IsDir() {
			return cand
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "plugins/ooda"
	}
	cur := cwd
	for {
		for _, sub := range []string{"", filepath.Join("plugins", "ooda")} {
			cand := filepath.Join(cur, sub)
			if st, serr := os.Stat(filepath.Join(cand, "plugin.json")); serr == nil && !st.IsDir() {
				return cand
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return filepath.Join(cwd, "plugins", "ooda")
}

// QuotaFilePath resolves quota.json via override, env vars, or XDG state home.
func QuotaFilePath(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("AGENT_QUOTA_FILE"); env != "" {
		return env
	}
	if env := os.Getenv("OODA_QUOTA_FILE"); env != "" {
		return env
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "ooda", "quota.json")
}

// DiscoveryFilePath resolves the cached CLI capability JSON path beside quota.json.
func DiscoveryFilePath() string {
	if env := os.Getenv("OODA_DISCOVERY_FILE"); env != "" {
		if strings.HasPrefix(env, "~/") {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, env[2:])
		}
		return env
	}
	return filepath.Join(filepath.Dir(QuotaFilePath("")), "discovery.json")
}

// HudDirPath returns the live HUD telemetry root directory.
func HudDirPath() string {
	if env := os.Getenv("ROUTER_HUD_DIR"); env != "" {
		return env
	}
	return "/tmp/router-hud"
}

// SlotsRootPath returns the worktree slot scratch directory.
func SlotsRootPath() string {
	if env := os.Getenv("ROUTER_SLOTS_DIR"); env != "" {
		return env
	}
	return "/tmp/router-slots"
}

// SafeID sanitizes a string for safe filenames, defaulting to "default".
func SafeID(raw string) string {
	cleaned := safeIDSubRe.ReplaceAllString(strings.TrimSpace(raw), "_")
	if cleaned == "" {
		return "default"
	}
	return cleaned
}

// RouterDepth returns nested worker dispatch depth from ROUTER_DEPTH.
func RouterDepth() int {
	raw := strings.TrimSpace(os.Getenv("ROUTER_DEPTH"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 1
	}
	if n < 0 {
		return 0
	}
	return n
}

// AtomicWriteJSON serializes v to JSON atomically via a sibling temp file.
func AtomicWriteJSON(path string, v any, compact bool, priv bool) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if priv {
		_ = os.Chmod(parent, 0o700)
	}
	var data []byte
	var err error
	if compact {
		data, err = json.Marshal(v)
	} else {
		data, err = json.MarshalIndent(v, "", "  ")
		if err == nil {
			data = append(data, '\n')
		}
	}
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if priv {
		mode = 0o600
	}
	tmp, err := os.CreateTemp(parent, fmt.Sprintf(".%s.tmp.%d.*", filepath.Base(path), os.Getpid()))
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
