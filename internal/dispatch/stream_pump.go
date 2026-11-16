package dispatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/UberMetroid/agent-ooda/internal/envelope"
	"github.com/UberMetroid/agent-ooda/internal/fleet"
)

const (
	outputTailRunes = 20000
	streamTick      = 1 * time.Second
)

// ExecReq specifies a single worker subprocess execution with streaming HUD updates.
type ExecReq struct {
	Provider, CLIPath, Model, Effort string
	Info                             *fleet.ProviderConfig
	Prompt, Worktree, SlotDir        string
	TaskID, ReviewMode, MarkerPath   string
	Files                            []string
	TimeoutSec                       int
	ReadOnly                         bool
	Marker                           WorkerMarker
}

// ExecRes holds normalized subprocess exit code, assistant output, timing, and usage.
type ExecRes struct {
	ExitCode   int
	Output     string
	TimedOut   bool
	DurationS  float64
	ExportInfo *fleet.ExportUsage
}

func nowFloatSec() float64 {
	return math.Round(float64(time.Now().UnixNano())/1e7) / 100
}

func tailRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[len(r)-limit:])
}

func safeKillGroup(mu *sync.Mutex, reaped *bool, p *os.Process) error {
	mu.Lock()
	defer mu.Unlock()
	if !*reaped && p != nil {
		*reaped = true
		return syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
	return nil
}

func pumpStream(r io.Reader, ndjson, checkStderrQuota bool, mu *sync.Mutex, st *streamTracker, onAbort func()) {
	br := bufio.NewReaderSize(r, 16*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			mu.Lock()
			if ndjson {
				st.consumeLine(line, nowFloatSec())
			} else {
				st.rawBuf.addStr(line)
				trimmed := strings.TrimSpace(line)
				low := strings.ToLower(trimmed)
				errFrame := checkStderrQuota || strings.HasPrefix(low, "error:") ||
					strings.HasPrefix(low, "api error") || strings.HasPrefix(low, "fatal:") || strings.HasPrefix(low, "{")
				if errFrame && IsLiveQuotaLine(trimmed) {
					st.isError, st.errText, st.qAbort = true, trimmed, true
				}
			}
			abort := st.qAbort
			mu.Unlock()
			if abort && onAbort != nil {
				onAbort()
			}
		}
		if err != nil {
			break
		}
	}
}

func runHudTicker(ctx context.Context, stop chan struct{}, req ExecReq, mu *sync.Mutex, st *streamTracker) {
	if req.MarkerPath == "" {
		return
	}
	ticker := time.NewTicker(streamTick)
	defer ticker.Stop()
	lastSig := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			now := nowFloatSec()
			mu.Lock()
			rate, exact := st.currentRate(now)
			phase := st.phase
			mu.Unlock()
			sig := fmt.Sprintf("%s|%.0f|%d", phase, rate, max(0, int(now-req.Marker.StartedAt)))
			if sig == lastSig {
				continue
			}
			lastSig = sig
			req.Marker.Phase, req.Marker.TokS, req.Marker.TokExact, req.Marker.UpdatedAt = phase, rate, exact, now
			HudWorkerUpdate(req.MarkerPath, req.Marker)
		}
	}
}

func finalizeOutput(built BuiltCmd, st *streamTracker, errText string,
	code int, timedOut bool, timeoutSec int) (string, int, *fleet.ExportUsage) {
	var exp *fleet.ExportUsage
	if st.usage.Tokens != nil || st.usage.CostUSD != nil {
		exp = &st.usage
	}
	if vibeExp := ParseVibeExport(built.VibeExportDir); vibeExp != nil {
		exp = vibeExp
	}
	out := st.rawBuf.str()
	if built.StreamFmt == "ndjson" {
		switch {
		case st.isError && st.errText != "":
			out = "[error] " + st.errText
		case st.hasFinal && st.finalResp != "":
			out = st.finalResp
		case len(st.cleanBuf.buf) > 0:
			out = st.cleanBuf.str()
		case len(st.turnBuf.buf) > 0:
			out = st.turnBuf.str()
		}
	}
	if st.isError && code == 0 {
		code = 1
	}
	if errText != "" && !strings.Contains(out, errText) {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += errText
	}
	if timedOut {
		out += fmt.Sprintf("\n<TIMEOUT after %ds>", timeoutSec)
	}
	return tailRunes(out, outputTailRunes), code, exp
}

func runStreamProcess(req ExecReq, built BuiltCmd, timeoutSec int) ExecRes {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, built.Argv[0], built.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = req.Worktree, built.Env, 2*time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var mu sync.Mutex
	var reaped bool
	cmd.Cancel = func() error { return safeKillGroup(&mu, &reaped, cmd.Process) }
	prOut, pwOut := io.Pipe()
	prErr, pwErr := io.Pipe()
	defer prOut.Close()
	defer prErr.Close()
	cmd.Stdout, cmd.Stderr = pwOut, pwErr
	if serr := cmd.Start(); serr != nil {
		_ = pwOut.Close()
		_ = pwErr.Close()
		return ExecRes{ExitCode: 1, Output: serr.Error(), DurationS: time.Since(start).Seconds()}
	}
	onAbort := func() { _ = safeKillGroup(&mu, &reaped, cmd.Process) }
	st, errSt := &streamTracker{phase: "wait"}, &streamTracker{}
	if built.StreamFmt == "ndjson" && req.MarkerPath != "" {
		req.Marker.Phase, req.Marker.UpdatedAt = "wait", nowFloatSec()
		HudWorkerUpdate(req.MarkerPath, req.Marker)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		pumpStream(prOut, built.StreamFmt == "ndjson", false, &mu, st, onAbort)
	}()
	go func() {
		defer wg.Done()
		pumpStream(prErr, false, true, &mu, errSt, onAbort)
	}()
	stopTick := make(chan struct{})
	go func() {
		defer wg.Done()
		runHudTicker(ctx, stopTick, req, &mu, st)
	}()
	werr := cmd.Wait()
	mu.Lock()
	reaped = true
	mu.Unlock()
	_ = pwOut.Close()
	_ = pwErr.Close()
	close(stopTick)
	wg.Wait()
	mu.Lock()
	qAborted, errText := st.qAbort || errSt.qAbort, strings.TrimSpace(errSt.rawBuf.str())
	mu.Unlock()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded) && !qAborted
	code := 1
	if timedOut {
		code = -1
	} else if !qAborted && werr == nil {
		code = 0
	} else if !qAborted && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		code = cmd.ProcessState.ExitCode()
	}
	out, finalCode, exp := finalizeOutput(built, st, errText, code, timedOut, timeoutSec)
	return ExecRes{ExitCode: finalCode, Output: out, TimedOut: timedOut,
		DurationS: time.Since(start).Seconds(), ExportInfo: exp}
}

// ExecuteOnce spawns one bounded worker process, streams live HUD telemetry, and returns output.
func ExecuteOnce(req ExecReq) ExecRes {
	built, err := BuildWorkerCommand(CmdReq{Provider: req.Provider, CLIPath: req.CLIPath,
		Model: req.Model, Effort: req.Effort, Prompt: req.Prompt, Files: req.Files, Worktree: req.Worktree,
		SlotDir: req.SlotDir, TaskID: req.TaskID, ReadOnly: req.ReadOnly, ReviewMode: req.ReviewMode})
	if err != nil {
		return ExecRes{ExitCode: 2, Output: err.Error()}
	}
	if built.PromptFile != "" {
		defer os.Remove(built.PromptFile)
	}
	if built.VibeExportDir != "" {
		defer os.RemoveAll(built.VibeExportDir)
	}
	if req.Provider == "agy-opus" && !slices.Contains(built.Argv, "--model") {
		if agyCfg := envelope.ProbeAgySettings(""); agyCfg.GeminiBlocked {
			return ExecRes{ExitCode: 2, Output: fmt.Sprintf("error: Gemini guardrail blocked agy-opus (%q) — "+
				"pass --model claude-opus-5-5-high or run `router --discover`.", agyCfg.SettingsModel)}
		}
	}
	return runStreamProcess(req, built, max(1, req.TimeoutSec))
}
