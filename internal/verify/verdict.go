package verify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

const pTagPat = `\[P[0-3](?:-[0-3])?\]`

var (
	acceptLineRe = regexp.MustCompile(
		`(?i)^\s*(?:\*\*|__)?VERDICT(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*ACCEPT\s*(?:\*\*|__)?\.?\s*$`,
	)
	mcodeFindingRe = regexp.MustCompile(`(?i)` + pTagPat)
	cleanTagRe     = regexp.MustCompile(
		`(?i)\b(?:none\s+of\s+(?:the\s+)?` + pTagPat +
			`\s+(?:(?:categories|levels|tags)\s+)?appl(?:y|ies)` +
			`|(?:no|zero|without|0)\s+` +
			`(?:(?:new|other|remaining|additional|qualifying|reportable|actionable)\s+)*` +
			`(?:\*\*|__)?(?:` + pTagPat + `(?:\s*(?:,\s*(?:or|and)|[-/,]|or|and)\s*|\s+))+` +
			`(?:\*\*|__)?(?:issues?|findings?|bugs?|defects?|problems?|regressions?|violations?))\b`,
	)
	cleanNoteRe = regexp.MustCompile(
		`(?i)^\s*no\s+(?:(?:reportable|actionable|qualifying)\s+)?issues(?:\s+were)?\s+found\.?\s*$`,
	)
)

// ParseReviewVerdict returns ACCEPT, REJECT, or UNKNOWN using fail-closed rules.
func ParseReviewVerdict(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	hasReject := false
	hasFinding := false
	var nonEmpty []string
	for _, ln := range lines {
		if m := fleet.VerdictRe.FindStringSubmatch(ln); len(m) == 2 {
			if strings.EqualFold(m[1], "REJECT") {
				hasReject = true
			}
		}
		scrubbed := cleanTagRe.ReplaceAllString(ln, "")
		if mcodeFindingRe.MatchString(scrubbed) {
			hasFinding = true
		}
		if strings.TrimSpace(ln) != "" {
			nonEmpty = append(nonEmpty, ln)
		}
	}
	if hasReject || hasFinding {
		return "REJECT"
	}
	if len(nonEmpty) == 0 {
		return "UNKNOWN"
	}
	if acceptLineRe.MatchString(nonEmpty[len(nonEmpty)-1]) {
		return "ACCEPT"
	}
	if len(nonEmpty) <= 3 {
		for _, ln := range nonEmpty {
			if cleanNoteRe.MatchString(ln) {
				return "ACCEPT"
			}
		}
	}
	return "UNKNOWN"
}

// BuildReviewPrompt builds the single-pass inline read-only patch review prompt.
func BuildReviewPrompt(
	targetDir string, dc DiffContext, scopeViol, declared []string,
) string {
	scopeNote := "(unspecified)"
	if len(declared) > 0 {
		scopeNote = strings.Join(declared, ", ")
	}
	violNote := "none"
	if len(scopeViol) > 0 {
		violNote = strings.Join(scopeViol, ", ")
	}
	return fmt.Sprintf(
		"Read-only independent code review of %s changes in %s. "+
			"The complete git status, diff stat, and patch are provided inline below. "+
			"Do NOT modify, create, or delete any files, and do NOT execute shell commands "+
			"or crawl unrelated repository files.\n\n"+
			"Declared files in scope: %s\n"+
			"Detected scope violations: %s\n"+
			"git status --short:\n%s\n\n"+
			"git diff --stat:\n%s\n\n"+
			"Patch:\n%s\n\n"+
			"Report concisely:\n"+
			"1. Logic bugs or broken invariants in the diff\n"+
			"2. Scope or unintended file modifications\n"+
			"3. Missing error handling or fail-open paths\n"+
			"4. Final line MUST be exactly: VERDICT: ACCEPT  or  VERDICT: REJECT - <reason>",
		dc.Source, targetDir, scopeNote, violNote, dc.Status, dc.DiffStat, dc.Diff,
	)
}

// BuildNativeReviewNote builds the bounded contract + patch note for mcode native review.
func BuildNativeReviewNote(
	targetDir string, dc DiffContext, scopeViol, declared []string,
) string {
	scopeNote := "(unspecified)"
	if len(declared) > 0 {
		scopeNote = strings.Join(declared, ", ")
	}
	violNote := "none"
	if len(scopeViol) > 0 {
		violNote = strings.Join(scopeViol, ", ")
	}
	status := dc.Status
	if status == "" {
		status = "(clean)"
	}
	stat := dc.DiffStat
	if stat == "" {
		stat = "(no tracked diff)"
	}
	diff := dc.Diff
	if diff == "" {
		diff = "(empty)"
	}
	return fmt.Sprintf(
		"Read-only independent code review of %s changes in %s. "+
			"The assembled patch below includes untracked files as synthetic hunks. "+
			"You may read surrounding lines of touched files if needed, but do NOT modify, "+
			"create, or delete any files; do NOT run state-mutating or arbitrary shell "+
			"commands; and do NOT crawl unrelated repository files.\n\n"+
			"Declared files in scope: %s\n"+
			"Detected scope violations: %s\n"+
			"git status --short:\n%s\n\n"+
			"git diff --stat:\n%s\n\n"+
			"Patch:\n%s\n\n"+
			"Tag any findings with [P0], [P1], [P2], or [P3] concisely. "+
			"Final line MUST be exactly: VERDICT: ACCEPT  or  VERDICT: REJECT - <reason>",
		dc.Source, targetDir, scopeNote, violNote, status, stat, diff,
	)
}
