// Binary agy-router is the multi-call router, statusLine HUD, hook, and setup entrypoint.
package main

import (
	"os"
	"path/filepath"

	"github.com/UberMetroid/agy-router/internal/cli"
	"github.com/UberMetroid/agy-router/internal/hud"
	"github.com/UberMetroid/agy-router/internal/install"
)

func dispatchSubcommand(prog string, args []string) int {
	switch prog {
	case "agy-router-hud", "agent-ooda-hud", "ooda-hud", "statusline.py":
		return hud.RunStatusLine()
	case "agy-router-hook", "agent-ooda-hook", "ooda-hook":
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
