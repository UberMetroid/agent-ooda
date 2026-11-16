package dispatch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/UberMetroid/agent-ooda/internal/fleet"
	"github.com/UberMetroid/agent-ooda/internal/verify"
	"github.com/UberMetroid/agent-ooda/internal/worktree"
)

// BatchOpts configures parallel manifest execution across git worktrees.
type BatchOpts struct {
	Lane, Model, Effort, Timeout string
	QuotaPath                    string
	Skills, ExcludeLanes         []string
	Jobs                         int
	NoStripe, NoReview, NoPivot  bool
	LogWriter                    io.Writer
	TaskRunner                   func(ExecReq) ExecRes
	ReviewRunner                 verify.ReviewRunner
	ResolveCLI                   func(string) string
}

// BatchReview is the serialized review section written into results.json.
type BatchReview struct {
	Ran      bool   `json:"ran"`
	Lane     string `json:"lane"`
	Model    string `json:"model"`
	Verdict  string `json:"verdict"`
	ExitCode *int   `json:"exit_code"`
	Reason   string `json:"reason,omitempty"`
	Output   string `json:"output"`
}

// BatchRecord summarizes one worktree task outcome in .worktrees/results.json.
type BatchRecord struct {
	ID              string        `json:"id"`
	Branch          string        `json:"branch"`
	Worktree        string        `json:"worktree"`
	Slot            string        `json:"slot"`
	RequestedLane   string        `json:"requested_lane"`
	Lane            string        `json:"lane"`
	CLI             string        `json:"cli"`
	Model           string        `json:"model"`
	Effort          string        `json:"effort"`
	FallbackFrom    string        `json:"fallback_from"`
	Attempts        []TaskAttempt `json:"attempts"`
	ExitCode        int           `json:"exit_code"`
	TimedOut        bool          `json:"timed_out"`
	DurationS       float64       `json:"duration_s"`
	ChangedFiles    []string      `json:"changed_files"`
	ScopeViolations []string      `json:"scope_violations"`
	DiffStat        string        `json:"diff_stat"`
	Output          string        `json:"output"`
	Review          BatchReview   `json:"review"`
}

type batchJob struct {
	idx                       int
	task                      worktree.TaskSpec
	root, wt, branch, baseRef string
	wtErr                     error
}

func reviewWorktreeTask(
	task worktree.TaskSpec, wt string, slotNum int,
	changed, viols []string, opts BatchOpts, authorLane string, dc verify.DiffContext,
) BatchReview {
	if opts.NoReview || (task.Review != nil && !*task.Review) {
		return BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "disabled"}
	}
	if len(changed) == 0 {
		return BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "no changes in worktree"}
	}
	revSlot, err := worktree.EnsureSlot(fmt.Sprintf("review-%d", slotNum))
	if err != nil {
		rc := 1
		return BatchReview{Ran: true, Model: fleet.ReviewModelEnv(), Verdict: "UNKNOWN", ExitCode: &rc, Reason: err.Error()}
	}
	res := ExecReview(verify.ReviewReq{TargetDir: wt, SlotDir: revSlot, DiffCtx: dc,
		TimeoutSec: fleet.ReviewTimeout, ScopeViols: viols, Declared: task.Files, LogWriter: opts.LogWriter,
		AuthorLane: authorLane, QuotaPath: opts.QuotaPath, Runner: opts.ReviewRunner, ResolveCLI: opts.ResolveCLI})
	rc := res.ExitCode
	return BatchReview{Ran: res.Ran, Lane: res.Lane, Model: res.Model, Verdict: res.Verdict,
		ExitCode: &rc, Reason: res.Reason, Output: res.Output}
}

func dedupMerge(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func processBatchTask(slotPool chan int, job batchJob, opts BatchOpts) BatchRecord {
	if job.wtErr != nil {
		return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: filepath.Base(job.wt),
			ExitCode: 1, Output: job.wtErr.Error(),
			Review: BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "worktree error"}}
	}
	slotNum := <-slotPool
	defer func() { slotPool <- slotNum }()
	slotDir, err := worktree.EnsureSlot(strconv.Itoa(slotNum))
	if err != nil {
		return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: filepath.Base(job.wt),
			ExitCode: -1, Output: err.Error(),
			Review: BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "slot error"}}
	}
	tCopy := job.task
	if tCopy.Complexity == "" {
		tCopy.Complexity = worktree.InferComplexity(tCopy.Prompt, tCopy.Files, tCopy.ReadOnly)
	}
	rawSkills := dedupMerge(job.task.Skills, worktree.ParseCSVArg(opts.Skills))
	tCopy.Skills = worktree.ResolveTaskSkills(rawSkills, tCopy.Prompt, tCopy.Files, job.root)
	excl := dedupMerge(job.task.ExcludeLanes, worktree.ParseCSVArg(opts.ExcludeLanes))
	stripe := &job.idx
	if opts.NoStripe {
		stripe = nil
	}
	res, runErr := RunTaskWithFallback(TaskReq{Task: tCopy, Worktree: job.wt, SlotDir: slotDir,
		Lane: opts.Lane, Model: opts.Model, Effort: opts.Effort, TimeoutRaw: opts.Timeout, QuotaPath: opts.QuotaPath,
		StripeIdx: stripe, IsParallel: true, Exclude: excl, NoPivot: opts.NoPivot, LogWriter: opts.LogWriter,
		Runner: opts.TaskRunner, ResolveCLI: opts.ResolveCLI})
	if runErr != nil {
		res = TaskRes{ExitCode: -1, Output: runErr.Error(), RequestedLane: opts.Lane}
	}
	var changed []string
	var dc verify.DiffContext
	stat := worktree.DiffStat(job.wt)
	if job.baseRef != "" && verify.HeadRef(job.wt) != job.baseRef {
		dc = verify.CollectDiffContext(job.wt, false, job.baseRef)
		changed, stat = dc.ChangedFiles, dc.DiffStat
	} else {
		changed = worktree.ChangedFilesIn(job.wt, nil)
		dc = verify.CollectDiffContext(job.wt, false)
	}
	viols := worktree.FindScopeViolations(changed, job.task.Files)
	rev := reviewWorktreeTask(job.task, job.wt, slotNum, changed, viols, opts, res.Lane, dc)
	relWT := filepath.Base(job.wt)
	if r, rerr := filepath.Rel(job.root, job.wt); rerr == nil {
		relWT = r
	}
	return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: relWT, Slot: filepath.Base(slotDir),
		RequestedLane: res.RequestedLane, Lane: res.Lane, CLI: res.CLI, Model: res.Model, Effort: res.Effort,
		FallbackFrom: res.FallbackFrom, Attempts: res.Attempts, ExitCode: res.ExitCode, TimedOut: res.TimedOut,
		DurationS: res.DurationS, ChangedFiles: changed, ScopeViolations: viols, DiffStat: stat,
		Output: res.Output, Review: rev}
}

func runBatchWorkers(root string, tasks []worktree.TaskSpec, opts BatchOpts, jobs int, outW io.Writer) []BatchRecord {
	slotPool := make(chan int, jobs)
	for n := 0; n < jobs; n++ {
		_, _ = worktree.EnsureSlot(strconv.Itoa(n))
		slotPool <- n
	}
	items := make([]batchJob, len(tasks))
	for i, t := range tasks {
		p, br, err := worktree.EnsureWorktree(root, t.ID)
		it := batchJob{idx: i, task: t, root: root, wt: p, branch: br, wtErr: err}
		if err != nil {
			it.wt, it.branch = filepath.Join(root, ".worktrees", t.ID), "ooda/"+t.ID
			fmt.Fprintf(outW, "[%s] worktree error: %v\n", t.ID, err)
		} else {
			it.baseRef = verify.HeadRef(p)
			fmt.Fprintf(outW, "[%s] worktree=%s branch=%s\n", t.ID, p, br)
		}
		items[i] = it
	}
	ch := make(chan int, len(tasks))
	for i := range tasks {
		ch <- i
	}
	close(ch)
	var mu sync.Mutex
	var results []BatchRecord
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				rec := processBatchTask(slotPool, items[i], opts)
				mu.Lock()
				results = append(results, rec)
				status := "OK"
				if rec.ExitCode != 0 {
					status = fmt.Sprintf("FAIL(%d)", rec.ExitCode)
				}
				viol := ""
				if len(rec.ScopeViolations) > 0 {
					viol = fmt.Sprintf(" SCOPE_VIOLATION(%d)", len(rec.ScopeViolations))
				}
				fmt.Fprintf(outW, "[%s] %s lane=%s effort=%s %.1fs review=%s%s\n%s\n---\n",
					rec.ID, status, rec.Lane, rec.Effort, rec.DurationS, rec.Review.Verdict, viol, rec.DiffStat)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return results
}

// RunManifestBatch fans tasks out across worktrees, writes results.json, and returns exit code.
func RunManifestBatch(root string, tasks []worktree.TaskSpec, opts BatchOpts) int {
	if opts.LogWriter == nil {
		opts.LogWriter = os.Stdout
	}
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	for _, lane := range worktree.ParseCSVArg(opts.ExcludeLanes) {
		if _, err := fleet.NormalizeLane(lane, false); err != nil {
			fmt.Fprintln(opts.LogWriter, err)
			return 2
		}
	}
	jobs := min(max(1, opts.Jobs), max(1, len(tasks)))
	_ = os.MkdirAll(filepath.Join(root, ".worktrees"), 0o755)
	results := runBatchWorkers(root, tasks, opts, jobs, opts.LogWriter)
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	outPath := filepath.Join(root, ".worktrees", "results.json")
	if werr := fleet.AtomicWriteJSON(outPath, results, false, false); werr != nil {
		fmt.Fprintf(opts.LogWriter, "error: write %s: %v\n", outPath, werr)
		return 1
	}
	fmt.Fprintf(opts.LogWriter, "wrote %s\n", outPath)
	var failed []string
	for _, r := range results {
		revBad := r.Review.ExitCode != nil && (r.Review.Verdict != "ACCEPT" || *r.Review.ExitCode != 0)
		if r.ExitCode != 0 || len(r.ScopeViolations) > 0 || r.Review.Verdict == "REJECT" || revBad {
			failed = append(failed, r.ID)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(opts.LogWriter, "%d task(s) failed/rejected: %s\n", len(failed), strings.Join(failed, ", "))
		return 1
	}
	fmt.Fprintf(opts.LogWriter, "all tasks exited 0\n")
	return 0
}
