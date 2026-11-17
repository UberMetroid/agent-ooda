// Command gates enforces agy-router Power-of-2 architecture and portability rules.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type gateFn func(root string) []string

func findRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	cur := wd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return wd
		}
		cur = parent
	}
}

func runSelected(root string, names []string, agyBin string) int {
	reg := map[string]gateFn{
		"portable": checkPortable,
		"line-cap": checkLineCap,
		"density":  checkDensity,
		"vocab":    checkVocab,
		"shape":    checkShape,
		"plugin-check": func(r string) []string {
			return checkPlugin(r, agyBin)
		},
	}
	failures := 0
	for _, name := range names {
		fn, ok := reg[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown gate: %s\n", name)
			return 2
		}
		viols := fn(root)
		if len(viols) > 0 {
			failures++
			fmt.Printf("FAIL [%s] (%d violation(s)):\n", name, len(viols))
			for _, v := range viols {
				fmt.Printf("  - %s\n", v)
			}
			continue
		}
		if name != "plugin-check" {
			fmt.Printf("PASS [%s]\n", name)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func main() {
	var gateList string
	var rootDir string
	var agyBin string
	flag.StringVar(&gateList, "gate", "all", "comma-separated gates or 'all'")
	flag.StringVar(&rootDir, "root", "", "repository root directory")
	flag.StringVar(&agyBin, "agy", "agy", "agy binary path for plugin-check")
	flag.Parse()

	if rootDir == "" {
		rootDir = findRepoRoot()
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	allGates := []string{"portable", "line-cap", "density", "vocab", "shape"}
	var selected []string
	if gateList == "all" {
		selected = allGates
	} else {
		for _, g := range strings.Split(gateList, ",") {
			if t := strings.TrimSpace(g); t != "" {
				selected = append(selected, t)
			}
		}
	}
	os.Exit(runSelected(absRoot, selected, agyBin))
}
