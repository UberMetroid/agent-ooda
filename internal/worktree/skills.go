package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

var (
	skillAliases = map[string]string{
		"ooda": "agent-ooda", "router": "agent-ooda", "agent-router": "agent-ooda",
	}
	fmNameRe      = regexp.MustCompile(`^\s*name\s*:\s*(.+)$`)
	fmDescRe      = regexp.MustCompile(`^\s*description\s*:\s*(.*)$`)
	discoverCache sync.Map
)

// SkillMeta describes a discoverable SKILL.md entry for reports and --skill.
type SkillMeta struct {
	Name string
	Desc string
	Core bool
}

// SkillRoots returns candidate skill directories in priority order.
func SkillRoots(cwd string) []string {
	base := cwd
	if base == "" {
		base, _ = os.Getwd()
	}
	ws := fleet.TryRepoRoot(base)
	if ws == "" {
		ws = base
	}
	home, _ := os.UserHomeDir()
	cands := []string{
		filepath.Join(ws, ".agents", "skills"),
		filepath.Join(fleet.PluginRootPath(), "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	pluginsDir := filepath.Join(home, ".gemini", "config", "plugins")
	if entries, err := os.ReadDir(pluginsDir); err == nil {
		var pRoots []string
		for _, ent := range entries {
			if ent.IsDir() {
				pRoots = append(pRoots, filepath.Join(pluginsDir, ent.Name(), "skills"))
			}
		}
		sort.Strings(pRoots)
		cands = append(cands, pRoots...)
	}
	seen := make(map[string]bool)
	var out []string
	for _, r := range cands {
		key := r
		if realR, err := filepath.EvalSymlinks(r); err == nil {
			key = realR
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

func parseSkillMeta(skillFile string) (string, string) {
	fallback := filepath.Base(filepath.Dir(skillFile))
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		return fallback, ""
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fallback, ""
	}
	end := len(lines)
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	name, desc := parseFrontmatter(lines[1:end], fallback)
	if runes := []rune(desc); len(runes) > 108 {
		desc = string(runes[:105]) + "..."
	}
	return name, desc
}

func parseFrontmatter(fm []string, fallback string) (string, string) {
	name := fallback
	var parts []string
	inDesc := false
	for _, line := range fm {
		if inDesc && (strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			if t := strings.TrimSpace(line); t != "" {
				parts = append(parts, t)
			}
			continue
		}
		inDesc = false
		if m := fmNameRe.FindStringSubmatch(line); len(m) == 2 {
			v := strings.Trim(strings.TrimSpace(m[1]), `"'`)
			if v != "" {
				name = v
			}
			continue
		}
		if m := fmDescRe.FindStringSubmatch(line); len(m) == 2 {
			val := strings.TrimSpace(m[1])
			if val == ">-" || val == ">" || val == "|-" || val == "|" {
				inDesc = true
			} else if val != "" {
				parts = append(parts, strings.Trim(val, `"'`))
			}
		}
	}
	return name, strings.Join(parts, " ")
}

// DiscoverSkills lists all SKILL.md packages sorted domain-first then core.
func DiscoverSkills(cwd string) []SkillMeta {
	roots := SkillRoots(cwd)
	cacheKey := strings.Join(roots, "\x00")
	if cached, ok := discoverCache.Load(cacheKey); ok {
		return append([]SkillMeta(nil), cached.([]SkillMeta)...)
	}
	seen := make(map[string]bool)
	var out []SkillMeta
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, ent := range entries {
			if !ent.IsDir() || seen[ent.Name()] {
				continue
			}
			skillMD := filepath.Join(root, ent.Name(), "SKILL.md")
			if st, err := os.Stat(skillMD); err != nil || st.IsDir() {
				continue
			}
			seen[ent.Name()] = true
			name, desc := parseSkillMeta(skillMD)
			out = append(out, SkillMeta{Name: name, Desc: desc, Core: fleet.CoreHiddenSkills[name]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Core != out[j].Core {
			return !out[i].Core
		}
		return out[i].Name < out[j].Name
	})
	discoverCache.Store(cacheKey, append([]SkillMeta(nil), out...))
	return out
}

func availableSkills(roots []string) string {
	seen := make(map[string]bool)
	var names []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() || seen[ent.Name()] {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, ent.Name(), "SKILL.md")); err == nil {
				seen[ent.Name()] = true
				names = append(names, ent.Name())
			}
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// LoadSkillBlocks reads SKILL.md bodies formatted for prompt injection.
func LoadSkillBlocks(names []string, cwd string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	roots := SkillRoots(cwd)
	var blocks []string
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		name = strings.TrimPrefix(name, "agent-ooda:")
		name = strings.TrimPrefix(name, "ooda:")
		if alias, ok := skillAliases[name]; ok {
			name = alias
		}
		if !fleet.SafeIDRe.MatchString(name) {
			return "", fleet.UsageErr("error: invalid skill name %q", raw)
		}
		var skillFile string
		for _, r := range roots {
			cand := filepath.Join(r, name, "SKILL.md")
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				skillFile = cand
				break
			}
		}
		if skillFile == "" {
			return "", fleet.UsageErr("error: unknown skill %q (available: %s)", raw, availableSkills(roots))
		}
		body, err := os.ReadFile(skillFile)
		if err != nil {
			return "", err
		}
		txt := strings.ReplaceAll(strings.TrimSpace(string(body)), "\r\n", "\n")
		if strings.HasPrefix(txt, "---\n") {
			if end := strings.Index(txt[4:], "\n---"); end >= 0 {
				txt = strings.TrimSpace(txt[4+end+4:])
			}
		}
		if r := []rune(txt); len(r) > 8192 {
			txt = string(r[:8192]) + "\n...[truncated]..."
		}
		blocks = append(blocks, fmt.Sprintf(
			"=== Skill Guidance: %s ===\n%s\n=== End Skill: %s ===",
			name, txt, name,
		))
	}
	return strings.Join(blocks, "\n\n") + "\n\n", nil
}
