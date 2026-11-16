package verify

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

const snapMaxBytes = 64 << 20

var maxSnapshotFiles = 8192

func fileFingerprint(full string) string {
	st, err := os.Lstat(full)
	if err != nil {
		return "missing"
	}
	if st.Mode()&os.ModeSymlink != 0 {
		dest, _ := os.Readlink(full)
		return "link:" + dest
	}
	if !st.Mode().IsRegular() {
		return fmt.Sprintf("mode:%o", st.Mode())
	}
	fh, err := os.Open(full)
	if err != nil {
		return "unreadable"
	}
	defer fh.Close()
	h := sha256.New()
	_, _ = io.Copy(h, io.LimitReader(fh, snapMaxBytes))
	if st.Size() > snapMaxBytes {
		if mid := st.Size()/2 - 32768; mid >= snapMaxBytes {
			if _, serr := fh.Seek(mid, io.SeekStart); serr == nil {
				_, _ = io.CopyN(h, fh, 65536)
			}
		}
		if _, serr := fh.Seek(-65536, io.SeekEnd); serr == nil {
			_, _ = io.Copy(h, fh)
		}
	}
	return fmt.Sprintf("%d:%o:%x", st.Size(), st.Mode().Perm(), h.Sum(nil))
}

func runtimeFilter(extra ...string) func(string) bool {
	qp := fleet.QuotaFilePath("")
	cands := []string{
		fleet.HudDirPath(), fleet.SlotsRootPath(),
		qp, strings.TrimSuffix(qp, filepath.Ext(qp)) + ".lock",
	}
	for _, p := range extra {
		if strings.TrimSpace(p) != "" {
			cands = append(cands, p, strings.TrimSuffix(p, filepath.Ext(p))+".lock")
		}
	}
	var ex []string
	for _, p := range cands {
		if a, err := filepath.Abs(p); err == nil && a != "" {
			ex = append(ex, a)
		}
	}
	return func(ap string) bool {
		for _, r := range ex {
			if ap == r || strings.HasPrefix(ap, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
}

func snapshotGit(root string, extra ...string) map[string]string {
	head := HeadRef(root)
	raw, ok := gitOutOK(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if !ok || head == "" {
		return map[string]string{"@ERR": "git-snapshot-failed"}
	}
	refs, _ := gitOutOK(root, "show-ref", "--head")
	rs := sha256.Sum256([]byte(refs))
	snap, count := map[string]string{"@HEAD": head, "@REFS": fmt.Sprintf("%x", rs[:16])}, 0
	skipRt := runtimeFilter(extra...)
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 4 {
			continue
		}
		xy, rawPath := rec[:2], rec[3:]
		rel, err := worktree.NormalizeRelPath(strings.TrimSuffix(rawPath, "/"))
		if full := filepath.Join(root, filepath.FromSlash(rel)); err == nil && rel != "" &&
			!worktree.IsIgnoredArtifact(rel) && !skipRt(full) {
			if count++; count > maxSnapshotFiles {
				return map[string]string{"@ERR": "too-many-files"}
			}
			snap[rel] = xy + "|" + fileFingerprint(full)
		}
		if (xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C') && i+1 < len(parts) {
			i++
			orig, oerr := worktree.NormalizeRelPath(strings.TrimSuffix(parts[i], "/"))
			if full := filepath.Join(root, filepath.FromSlash(orig)); oerr == nil && orig != "" &&
				!worktree.IsIgnoredArtifact(orig) && !skipRt(full) {
				snap["orig:"+orig] = xy + "|" + fileFingerprint(full)
			}
		}
	}
	return snap
}

func snapshotDir(dir string, extra ...string) map[string]string {
	absDir, aerr := filepath.Abs(dir)
	if aerr == nil {
		dir = absDir
	}
	snap, count, skipRt := map[string]string{"@MODE": "dir"}, 0, runtimeFilter(extra...)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		if rel = filepath.ToSlash(rel); rel == "." {
			return nil
		}
		if d.Name() == ".git" || worktree.IsIgnoredArtifact(rel) || skipRt(p) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if count++; count > maxSnapshotFiles {
			return fmt.Errorf("too many files")
		}
		snap[rel] = fileFingerprint(p)
		return nil
	})
	if err != nil {
		return map[string]string{"@ERR": "walk-failed"}
	}
	return snap
}

// SnapshotRepo captures repo-wide git HEAD/refs/porcelain state or bounded non-git directory state.
func SnapshotRepo(dir string, extra ...string) map[string]string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if root := fleet.TryRepoRoot(dir); root != "" {
		return snapshotGit(root, extra...)
	}
	return snapshotDir(dir, extra...)
}

// SnapshotDrift fails closed on snapshot errors and returns sorted modified/mutated paths.
func SnapshotDrift(before, after map[string]string) []string {
	if before == nil && after == nil {
		return nil
	}
	if len(before) == 0 || len(after) == 0 || before["@ERR"] != "" || after["@ERR"] != "" {
		return []string{"@SNAPSHOT_FAILED (unable to verify read-only workspace)"}
	}
	seen := make(map[string]bool, len(before)+len(after))
	for _, m := range []map[string]string{before, after} {
		for k := range m {
			seen[k] = true
		}
	}
	lbls := map[string]string{
		"@HEAD": "@HEAD (unexpected git commit)",
		"@REFS": "@REFS (unexpected git ref/branch/stash change)",
	}
	var drift []string
	for k := range seen {
		if before[k] == after[k] {
			continue
		}
		if lbl, ok := lbls[k]; ok {
			drift = append(drift, lbl)
		} else {
			drift = append(drift, k)
		}
	}
	sort.Strings(drift)
	return drift
}
