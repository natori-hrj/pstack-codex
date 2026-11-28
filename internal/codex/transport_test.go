package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/natori-hrj/nstack/internal/workflow"
)

func waitForPID(ctx context.Context, path string) (int, error) {
	ticks := time.NewTicker(10 * time.Millisecond)
	defer ticks.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) != 0 {
			return strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticks.C:
		}
	}
}

func TestTurnTeardown(t *testing.T) {
	installProtocolDouble(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	for _, test := range []struct {
		name, scenario string
		want           error
	}{
		{"abnormal EOF", "turn-eof", nil},
		{"timeout", "turn-hang", context.DeadlineExceeded},
		{"cancellation", "turn-hang", context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			pidFile := filepath.Join(root, "pid")
			t.Setenv("NSTACK_REVIEW_TEST_SCENARIO", test.scenario)
			t.Setenv("NSTACK_TEST_CHILD_PID", pidFile)
			ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer cancel()
			a := &Adapter{timeout: 5 * time.Second}
			if test.want == context.DeadlineExceeded {
				a.timeout = 2 * time.Second
			}
			done := make(chan error, 1)
			go func() {
				_, err := a.Run(ctx, workflow.TurnRequest{
					Directory: root, Role: Defaults().Reviewer, Prompt: "fixture",
					Schema: []byte(`{"type":"object"}`), Evidence: filepath.Join(root, "evidence"),
				})
				done <- err
			}()
			pid, err := waitForPID(ctx, pidFile)
			if err != nil {
				t.Fatal(err)
			}
			fd := trackFixtureProcess(t, pid)
			armFixtureArgCleanup(t, fd, pid, pidFile)
			if test.want == nil {
				fixtureWrite(pidFile+".exit", "")
			}
			if test.want == context.Canceled {
				cancel()
			}
			got := <-done
			if test.want == nil {
				if got == nil || !strings.Contains(got.Error(), "EOF") {
					t.Fatalf("expected EOF, got %v", got)
				}
			} else if !errors.Is(got, test.want) {
				t.Fatalf("expected %v, got %v", test.want, got)
			}
			assertReaped(t, fd)
		})
	}
}

func TestCancelledGateStopsChild(t *testing.T) {
	installProtocolDouble(t)
	home, checkout, scratch := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	fixtureWrite(filepath.Join(home, "config.toml"), "")
	pidFile := filepath.Join(scratch, "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := &Adapter{timeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() {
		result, err := a.Gate(ctx, workflow.GateRequest{
			Directory: checkout, Scratch: scratch, Evidence: filepath.Join(scratch, "gate"),
			Argv: []string{"/bin/sh", "-c", `printf '%s\n' "$$" > "$1"; /bin/sleep 30`, "gate", pidFile},
		})
		if err == nil && result.ExitCode != nil {
			err = fmt.Errorf("cancelled gate returned terminal code %d", *result.ExitCode)
		}
		done <- err
	}()
	pid, err := waitForPID(ctx, pidFile)
	if err != nil {
		t.Fatal(err)
	}
	fd := trackFixtureProcess(t, pid)
	armFixtureArgCleanup(t, fd, pid, pidFile)
	cancel()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not preserved: %v", err)
	}
	assertReaped(t, fd)
}
