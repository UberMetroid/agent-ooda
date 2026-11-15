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

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func pumpStream(r io.Reader, ndjson bool, mu *sync.Mutex, st *streamTracker) {
	if !ndjson {
		_, _ = io.Copy(&st.rawBuf, r)
		return
	}
	br := bufio.NewReaderSize(r, 16*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			mu.Lock()
			st.consumeLine(line, nowFloatSec())
			mu.Unlock()
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
			elapsed := max(0, int(now-req.Marker.StartedAt))
			sig := fmt.Sprintf("%s|%.0f|%d", phase, rate, elapsed)
			if sig == lastSig {
				continue
			}
			lastSig = sig
			m := req.Marker
			m.Phase, m.TokS, m.TokExact, m.UpdatedAt = phase, rate, exact, now
			HudWorkerUpdate(req.MarkerPath, m)
		}
	}
}

func finalizeOutput(
	built BuiltCmd, st *streamTracker, errText string,
	code int, timedOut bool, timeoutSec int,
) (string, int, *fleet.ExportUsage) {
	var exp *fleet.ExportUsage
	if st.usage.Tokens != nil || st.usage.CostUSD != nil {
		u := st.usage
		exp = &u
	}
	if built.VibeExportDir != "" {
		if vibeExp := ParseVibeExport(built.VibeExportDir); vibeExp != nil {
			exp = vibeExp
		}
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
	cmd.Dir, cmd.Env = req.Worktree, built.Env
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
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
	var mu sync.Mutex
	st := &streamTracker{phase: "wait"}
	if built.StreamFmt == "ndjson" && req.MarkerPath != "" {
		m := req.Marker
		m.Phase, m.UpdatedAt = "wait", nowFloatSec()
		HudWorkerUpdate(req.MarkerPath, m)
	}
	var errBuf tailBuf
	var wg, tickWg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pumpStream(prOut, built.StreamFmt == "ndjson", &mu, st)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&errBuf, prErr)
	}()
	stopTick := make(chan struct{})
	tickWg.Add(1)
	go func() {
		defer tickWg.Done()
		runHudTicker(ctx, stopTick, req, &mu, st)
	}()
	werr := cmd.Wait()
	_ = pwOut.Close()
	_ = pwErr.Close()
	wg.Wait()
	close(stopTick)
	tickWg.Wait()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	code := 0
	if timedOut {
		code = -1
	} else if werr != nil {
		code = 1
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			code = cmd.ProcessState.ExitCode()
		}
	}
	out, finalCode, exp := finalizeOutput(built, st, strings.TrimSpace(errBuf.str()), code, timedOut, timeoutSec)
	return ExecRes{
		ExitCode: finalCode, Output: out, TimedOut: timedOut, DurationS: time.Since(start).Seconds(), ExportInfo: exp,
	}
}

// ExecuteOnce spawns one bounded worker process, streams live HUD telemetry, and returns output.
func ExecuteOnce(req ExecReq) ExecRes {
	timeoutSec := max(1, req.TimeoutSec)
	built, err := BuildWorkerCommand(CmdReq{
		Provider: req.Provider, CLIPath: req.CLIPath, Model: req.Model, Effort: req.Effort,
		Prompt: req.Prompt, Files: req.Files, Worktree: req.Worktree,
		SlotDir: req.SlotDir, TaskID: req.TaskID, ReadOnly: req.ReadOnly, ReviewMode: req.ReviewMode,
	})
	if err != nil {
		return ExecRes{ExitCode: 2, Output: err.Error()}
	}
	if built.PromptFile != "" {
		defer os.Remove(built.PromptFile)
	}
	if built.VibeExportDir != "" {
		defer os.RemoveAll(built.VibeExportDir)
	}
	if req.Provider == "agy-opus" && !hasFlag(built.Argv, "--model") {
		if agyCfg := envelope.ProbeAgySettings(""); agyCfg.GeminiBlocked {
			return ExecRes{ExitCode: 2, Output: fmt.Sprintf("error: Gemini guardrail blocked agy-opus — "+
				"settings.json model %q is Gemini/Orchestrator; pass --model claude-opus-5-5-high "+
				"or run `router --discover`.", agyCfg.SettingsModel)}
		}
	}
	return runStreamProcess(req, built, timeoutSec)
}
