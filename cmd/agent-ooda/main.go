// Binary agent-ooda is the multi-call router, Bubble-Canopy HUD, hook, and setup entrypoint.
package main

import (
	"os"
	"path/filepath"

	"github.com/UberMetroid/agent-ooda/internal/cli"
	"github.com/UberMetroid/agent-ooda/internal/hud"
	"github.com/UberMetroid/agent-ooda/internal/install"
)

func dispatchSubcommand(prog string, args []string) int {
	switch prog {
	case "agent-ooda-hud", "ooda-hud", "statusline.py":
		return hud.RunStatusLine()
	case "agent-ooda-hook", "ooda-hook":
		return hud.RunHook()
	}
	if len(args) > 0 {
		switch args[0] {
		case "__hud":
			return hud.RunStatusLine()
		case "__hook":
			return hud.RunHook()
		case "__wire":
			return install.RunWireCmd(args[1:], os.Stdout, os.Stderr)
		case "__setup":
			return install.RunSetupCmd(args[1:], os.Stderr)
		}
	}
	return cli.RunCLI(prog, args, os.Stdout, os.Stderr)
}

func main() {
	os.Exit(dispatchSubcommand(filepath.Base(os.Args[0]), os.Args[1:]))
}
