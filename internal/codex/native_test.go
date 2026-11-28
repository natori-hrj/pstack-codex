package codex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/natori-hrj/nstack/internal/install"
	"github.com/natori-hrj/nstack/internal/workflow"
)

func requireNative(t *testing.T, mode string) {
	t.Helper()
	if os.Getenv("NSTACK_CODEX_NATIVE_TEST") != mode {
		t.Skipf("set NSTACK_CODEX_NATIVE_TEST=%s to run against Codex 0.172.0", mode)
	}
	got, err := exec.Command("codex", "--version").Output()
	if err != nil || strings.TrimSpace(string(got)) != "codex-cli 0.172.0" {
		t.Fatalf("need Codex 0.172.0: %q, %v", got, err)
	}
}

func TestNativeOffline(t *testing.T) {
	requireNative(t, "offline")
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	const config = "[mcp_servers.tool]\ncommand = \"false\"\n" +
		"[mcp_servers.\"fixture.with.dot\"]\ncommand = \"false\"\n" +
		"[mcp_servers.disabled]\ncommand = \"false\"\nenabled = false\n"
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cwd, evidence := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, disabled, err := open(ctx, cwd, filepath.Join(evidence, "config"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if disabled != 2 {
		t.Fatalf("disabled MCP servers = %d, want 2", disabled)
	}
	if err := s.verifyOAuth(); err == nil || !strings.Contains(err.Error(), "no authenticated account") {
		t.Fatalf("anonymous account check = %v", err)
	}
	network := false
	proof, err := s.probeReadOnly(cwd, sandbox{Type: "readOnly", NetworkAccess: &network})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.probeScratch(cwd, evidence); err != nil {
		t.Fatal(err)
	}
	outside, scratch := t.TempDir(), t.TempDir()
	pathOutside := filepath.Join(outside, "must-not-exist")
	result, err := s.gate(cwd, []string{"/bin/sh", "-c", `: > "$1"`, "nstack-probe", pathOutside}, scratch)
	if err != nil || *result.ExitCode == 0 {
		t.Fatalf("native gate did not deny a write outside checkout and scratch: %v", err)
	}
	if _, err := os.Lstat(pathOutside); !os.IsNotExist(err) {
		t.Fatalf("unexpected outside write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != config {
		t.Fatal("native configuration changed during discovery")
	}
	t.Logf("native config, no OAuth, read-only denial (%s), and confined workspace scratch verified", proof)
}

func TestNativeOfflineDiscovery(t *testing.T) {
	requireNative(t, "offline")
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	evidence := t.TempDir()
	installCLI := func(action, id string) {
		t.Helper()
		if code := install.RunCLI([]string{"--home", home, action, id}, os.Stdout, os.Stderr); code != 0 {
			t.Fatalf("install %s %s: %d", action, id, code)
		}
	}
	installCLI("stage", "first")
	installCLI("stage", "second")
	for i, id := range []string{"first", "second", "first"} {
		installCLI("activate", id)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := startServer(ctx, cwd, filepath.Join(evidence, fmt.Sprintf("discover-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Data []struct {
				Skills []struct {
					Enabled bool   `json:"enabled"`
					Name    string `json:"name"`
					Path    string `json:"path"`
				} `json:"skills"`
			} `json:"data"`
		}
		err = s.request("skills/list", object{"cwds": []string{cwd}}, &list, nil)
		err = errors.Join(err, s.close())
		if err != nil {
			t.Fatal(err)
		}
		skills := filepath.Join(home, "nstack", "snapshots", id, "skills")
		expected, err := fs.Glob(os.DirFS(skills), "*/SKILL.md")
		if err != nil || len(expected) != 26 {
			t.Fatalf("installed catalog size=%d: %v", len(expected), err)
		}
		found := map[string][]string{}
		for _, group := range list.Data {
			for _, skill := range group.Skills {
				if skill.Enabled {
					resolved, err := filepath.EvalSymlinks(skill.Path)
					if err != nil {
						t.Fatal(err)
					}
					found[skill.Name] = append(found[skill.Name], resolved)
				}
			}
		}
		for _, path := range expected {
			name := filepath.Dir(path)
			if paths := found[name]; len(paths) != 1 || paths[0] != filepath.Join(skills, path) {
				t.Errorf("%s: %s resolved to %v", id, name, paths)
			}
		}
		for _, old := range []string{"poteto-mode", "setup-pstack"} {
			if len(found[old]) != 0 {
				t.Errorf("legacy skill %s was discovered", old)
			}
		}
	}
}

func TestNativeOAuth(t *testing.T) {
	requireNative(t, "oauth")
	cwd, evidence := t.TempDir(), t.TempDir()
	a := &Adapter{timeout: 90 * time.Second}
	ctx := context.Background()
	writer, reviewer := Defaults().Writer, Defaults().Reviewer
	writer.Name, reviewer.Name = "nstack_implement", "nstack_review"
	probe, err := a.Preflight(ctx, cwd, reviewer, filepath.Join(evidence, "preflight"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native preflight: %v", probe)
	result, err := a.Run(ctx, workflow.TurnRequest{
		Directory: cwd, Role: writer, Evidence: filepath.Join(evidence, "writer"),
		Prompt: "In this disposable checkout, create result.txt containing exactly 'ready' and a newline. Then report completion.",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "result.txt"))
	if err != nil || string(data) != "ready\n" {
		t.Fatalf("writer result = %q, %v", data, err)
	}
	t.Logf("writer model=%s effort=%s session=%s turn=%v", result.Model, result.Effort, result.Session, result.Evidence["turn"])
	review, err := a.Run(ctx, workflow.TurnRequest{
		Directory: cwd, Role: reviewer, Evidence: filepath.Join(evidence, "review"),
		Prompt: "Read result.txt. If it contains ready followed by a newline, output UNCONSTRAINED_TEST_MARKER as plain text, with no quotes and no JSON.",
		Schema: json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string","enum":["pass"]}},"required":["verdict"],"additionalProperties":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := decode([]byte(review.Message), &response); err != nil || len(response) != 1 || response["verdict"] != "pass" || review.Session == result.Session {
		t.Fatalf("native schema or session isolation not demonstrated: %q, %v", review.Message, err)
	}
	t.Logf("reviewer model=%s effort=%s session=%s turn=%v writeProbe=%v verdict=%s",
		review.Model, review.Effort, review.Session, review.Evidence["turn"], review.Evidence["writeProbe"], response["verdict"])
}

func TestNativeOfflineCancellation(t *testing.T) {
	requireNative(t, "offline")
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	cwd, evidence := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, err := open(ctx, cwd, filepath.Join(evidence, "cancel"), false)
	if err != nil {
		t.Fatal(err)
	}
	var id [5]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("ns%x", id)
	probe := filepath.Join(evidence, name)
	program, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(probe, program, 0700); err != nil {
		t.Fatal(err)
	}
	type commandOutcome struct{ commandErr, cleanupErr error }
	done := make(chan commandOutcome, 1)
	go func() {
		defer close(done)
		result, err := s.gate(cwd, []string{probe, "15"}, evidence)
		if err == nil && result.ExitCode != nil && *result.ExitCode != 0 {
			err = fmt.Errorf("native child command exited %d: %s", *result.ExitCode, *result.Stderr)
		}
		cleanup := s.close()
		if s.close() != cleanup {
			cleanup = errors.New("repeated cleanup did not preserve its result")
		}
		done <- commandOutcome{err, cleanup}
	}()
	// The sandbox uses a PID namespace. Observe the actual host PID by
	// matching the unique test executable's comm value.
	var pid int
	for pid == 0 {
		paths, err := filepath.Glob("/proc/[0-9]*/comm")
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err == nil && strings.TrimSpace(string(raw)) == name {
				pid, _ = strconv.Atoi(filepath.Base(filepath.Dir(path)))
				break
			}
		}
		if pid == 0 {
			select {
			case outcome := <-done:
				t.Fatalf("native command ended before its child was observed: %v", errors.Join(outcome.commandErr, outcome.cleanupErr))
			case <-ctx.Done():
				outcome := <-done
				t.Fatalf("could not find native child: %v (command error: %v)", ctx.Err(), errors.Join(outcome.commandErr, outcome.cleanupErr))
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	fd := trackFixtureProcess(t, pid)
	armFixtureCleanup(t, fd, pid, func() bool {
		want, wantErr := os.Stat(probe)
		actual, actualErr := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
		return wantErr == nil && actualErr == nil && os.SameFile(want, actual)
	})
	cancel()
	outcome := <-done
	if !errors.Is(outcome.commandErr, context.Canceled) || outcome.cleanupErr != nil {
		t.Fatalf("expected cancellation and confirmed cleanup, got command=%v cleanup=%v", outcome.commandErr, outcome.cleanupErr)
	}
	assertReaped(t, fd)
	t.Logf("native child %d observed, stopped and reaped before cancellation completed", pid)
}
