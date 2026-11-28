// Command nstack-codex builds the standalone nstack distribution.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/natori-hrj/nstack"
	"github.com/natori-hrj/nstack/internal/codex"
	"github.com/natori-hrj/nstack/internal/install"
	"github.com/natori-hrj/nstack/internal/workflow"
)

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s {install|review|check-role|--version} [arguments]\n", filepath.Base(os.Args[0]))
		return 2
	}
	switch os.Args[1] {
	case codex.SupervisorArgument:
		if err := codex.Supervise(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "Codex process supervisor:", err)
			return 1
		}
		return 0
	case "review":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return workflow.RunCLI(ctx, os.Args[2:], os.Stdout, os.Stderr, codex.Defaults(), codex.New)
	case "check-role":
		return codex.CheckRoleCLI(os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	case "install":
		return install.RunCLI(os.Args[2:], os.Stdout, os.Stderr)
	case "--version":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "--version takes no arguments")
			return 2
		}
		assets, err := distribution.Bundled()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("nstack %s assets sha256:%s\n", assets.Version, assets.Revision)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "expected install, review, check-role, or --version")
		return 2
	}
}

func main() { os.Exit(run()) }
