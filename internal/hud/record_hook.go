package hud

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

var (
	skillPathRe = regexp.MustCompile(`/skills/([A-Za-z0-9._-]+)/SKILL\.md$`)
	routerCmdRe = regexp.MustCompile(`(?:^|[\s/;|&"'])(agent-ooda|ooda|router|dispatch\.py)(?:\s|$|"|')`)
	skillFlagRe = regexp.MustCompile(`--skills?(?:=|\s+)([A-Za-z0-9.,:_-]+)`)
)

type sessionRecord struct {
	ConversationID string   `json:"conversation_id"`
	Skills         []string `json:"skills"`
	ModelName      string   `json:"model_name"`
	Cwd            string   `json:"cwd"`
	UpdatedAt      float64  `json:"updated_at"`
}

func appendSkill(skills []string, raw string) []string {
	name := strings.TrimSpace(raw)
	name = strings.TrimPrefix(name, "agent-ooda:")
	name = strings.TrimPrefix(name, "ooda:")
	if name == "agent-router" {
		name = "router"
	}
	if name == "" || IgnoredSkills[name] {
		return skills
	}
	for _, s := range skills {
		if s == name {
			return skills
		}
	}
	return append(skills, name)
}

// ApplyToolCall updates tracked skills from a PostToolUse hook payload.
func ApplyToolCall(skills []string, payload map[string]any) []string {
	tool, _ := payload["toolCall"].(map[string]any)
	tname, _ := tool["name"].(string)
	args, _ := tool["args"].(map[string]any)
	if tname == "read_file" || tname == "view_file" {
		p := ""
		for _, k := range []string{"Path", "AbsolutePath", "path"} {
			if s, _ := args[k].(string); s != "" {
				p = s
				break
			}
		}
		if m := skillPathRe.FindStringSubmatch(p); len(m) == 2 {
			return appendSkill(skills, m[1])
		}
		return skills
	}
	if tname != "run_command" {
		return skills
	}
	cmdStr, _ := args["CommandLine"].(string)
	if cmdStr == "" {
		cmdStr, _ = args["command"].(string)
	}
	if !routerCmdRe.MatchString(cmdStr) {
		return skills
	}
	matches := skillFlagRe.FindAllStringSubmatch(cmdStr, -1)
	if len(matches) == 0 {
		return skills
	}
	for _, m := range matches {
		for _, part := range strings.Split(m[1], ",") {
			skills = appendSkill(skills, part)
		}
	}
	return skills
}

func handleHookEvent(payload map[string]any) {
	ev, _ := payload["hookEventName"].(string)
	if ev != "SessionStart" && ev != "PostToolUse" {
		return
	}
	convID, _ := payload["conversationId"].(string)
	if convID == "" {
		convID, _ = payload["conversation_id"].(string)
	}
	if convID == "" {
		convID = "default"
	}
	base := fleet.HudDirPath()
	sessFile := filepath.Join(base, "sessions", fleet.SafeID(convID)+".json")
	var prev sessionRecord
	if raw, err := os.ReadFile(sessFile); err == nil {
		_ = json.Unmarshal(raw, &prev)
	}
	var skills []string
	for _, s := range prev.Skills {
		skills = appendSkill(skills, s)
	}
	if ev == "PostToolUse" {
		skills = ApplyToolCall(skills, payload)
	}
	modelName, _ := payload["modelName"].(string)
	if modelName == "" {
		modelName = prev.ModelName
	}
	cwd, _ := payload["cwd"].(string)
	if cwd == "" {
		cwd = prev.Cwd
	}
	nowTS := math.Round(float64(time.Now().UnixNano())/1e7) / 100
	if skills == nil {
		skills = []string{}
	}
	rec := sessionRecord{
		ConversationID: convID, Skills: skills, ModelName: modelName,
		Cwd: cwd, UpdatedAt: nowTS,
	}
	_ = fleet.AtomicWriteJSON(sessFile, rec, true, true)
	ptr := map[string]any{"conversation_id": convID, "updated_at": nowTS}
	_ = fleet.AtomicWriteJSON(filepath.Join(base, "current_session.json"), ptr, true, true)
}

// HandleHookStdin processes one hook event from r and writes "{}\n" to w.
func HandleHookStdin(r io.Reader, w io.Writer) {
	if fleet.RouterDepth() < 1 {
		if raw, err := io.ReadAll(r); err == nil {
			s := strings.TrimSpace(string(raw))
			var payload map[string]any
			if s != "" && json.Unmarshal([]byte(s), &payload) == nil && payload != nil {
				handleHookEvent(payload)
			}
		}
	}
	_, _ = io.WriteString(w, "{}\n")
}

// RunHook is the CLI entrypoint for agent-ooda-hook.
func RunHook() int {
	HandleHookStdin(os.Stdin, os.Stdout)
	return 0
}
