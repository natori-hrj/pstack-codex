package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/natori-hrj/nstack/internal/workflow"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func readEvidence(t *testing.T, dir, name string) object {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var evidence object
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

// Cases and expected diagnostics are from the v0.4.0 parity/protocol runner.
func TestReviewProtocolContract(t *testing.T) {
	installProtocolDouble(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("OPENAI_API_KEY", "fixture-not-a-key")
	t.Setenv("CODEX_API_KEY", "fixture-not-a-key")
	const (
		writer = ""
		first  = "--review-first"
		only   = "--review-only"
	)
	for _, c := range []struct {
		name, mode, error string
		writes, reviews   int
	}{
		{"success", writer, "", 2, 2},
		{"review-first-detached", first, "", 0, 1},
		{"review-first-two-rounds", first, "remediation budget exhausted", 2, 3},
		{"review-first-pass", first, "", 0, 1},
		{"review-first-fast-forward", first, "", 0, 1},
		{"review-first-diverged", first, "merge-base", 0, 1},
		{"review-first-remediation-fast-forward", first, "", 1, 2},
		{"review-first-fix", first, "", 1, 2},
		{"review-first-repeat", first, "recurs or remains uncertain", 1, 2},
		{"review-first-uncertain", first, "recurs or remains uncertain", 1, 2},
		{"review-first-unknown", first, "unexpected previous", 1, 2},
		{"review-first-missing", first, "assess every previous", 1, 2},
		{"review-first-no-auth", first, "explicit refs/heads/", 0, 1},
		{"review-first-collision", first, "work ref must be new", 0, 1},
		{"remote-default", first, "remote default", 0, 1},
		{"protected-delivery-main", only, "isolated feature branch", 0, 1},
		{"protected-delivery-master", only, "isolated feature branch", 0, 1},
		{"protected-delivery-trunk", only, "isolated feature branch", 0, 1},
		{"delivery-mismatch", first, "starting candidate", 0, 1},
		{"delivery-none", only, "", 0, 1},
		{"work-none", first, "", 1, 2},
		{"local-no-auth", writer, "explicit refs/heads/", 0, 0},
		{"writer-ref-changed", writer, "work ref or candidate changed", 1, 0},
		{"destination-changed", writer, "effective delivery destination changed", 1, 0},
		{"review-first-zero", first, "remediation budget exhausted", 0, 1},
		{"review-first-gate", first, "quality gate failed", 0, 0},
		{"review-first-blocked", first, "review blocked", 0, 1},
		{"review-only", only, "", 0, 1},
		{"review-only-fast-forward", only, "", 0, 1},
		{"delivery-ref-suffix", only, "", 0, 1},
		{"review-only-changes", only, "requested changes in review-only mode", 0, 1},
		{"rewritten", writer, "", 2, 2},
		{"detached-default", writer, "cannot verify a non-default remote branch", 0, 0},
		{"assume-unchanged", writer, "checkout has index flags", 0, 0},
		{"skip-worktree", writer, "checkout has index flags", 0, 0},
		{"unauthenticated", writer, "no authenticated account", 0, 0},
		{"model", writer, "absent from the active model catalog", 0, 0},
		{"permission", writer, "permitted writing", 0, 0},
		{"mcp-forced", writer, "MCP overrides did not take effect", 0, 0},
		{"gate-scratch", writer, "could not verify quality gate scratch writes", 0, 0},
		{"wrong-policy", writer, "unexpected sandbox policy", 0, 0},
		{"sha", writer, "does not match the candidate", 1, 1},
		{"reroute", writer, "reported a model reroute", 1, 1},
		{"repeat", writer, "recurs or remains uncertain", 2, 2},
		{"budget", writer, "remediation budget exhausted", 1, 1},
		{"dirty", writer, "dirty checkout", 0, 0},
		{"gate", writer, "quality gate failed", 1, 0},
		{"api-key-account", only, "requires the existing ChatGPT OAuth", 0, 0},
		{"account-case-alias", only, "noncanonical Codex field", 0, 0},
		{"model-case-alias", only, "noncanonical Codex field", 0, 0},
		{"wrong-model", only, "unexpected model, context, or approval settings", 0, 0},
		{"wrong-effort", only, "unexpected model, context, or approval settings", 0, 0},
		{"missing-session", only, "unexpected model, context, or approval settings", 0, 0},
		{"missing-thread-lineage", only, "unexpected model, context, or approval settings", 0, 0},
		{"missing-turn", only, "missing turn ID", 0, 1},
		{"missing-terminal-error", only, "did not complete the turn unambiguously", 0, 1},
		{"malformed-verdict", only, "invalid structured review result", 0, 1},
		{"duplicate-turn-response", only, "duplicate Codex response ID", 0, 1},
		{"late-verdict", only, "item completed after the turn ended", 0, 1},
		{"unexpected-id", only, "unexpected or duplicate Codex response ID", 0, 0},
		{"missing-response", only, "invalid Codex JSON-RPC response", 0, 0},
		{"invalid-json", only, "invalid Codex JSON-RPC frame", 0, 0},
		{"partial-frame", only, "partial Codex frame at EOF", 0, 0},
		{"missing-writer-roots", writer, "did not report the implementation sandbox writable roots", 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := c.name
			root := t.TempDir()
			repo, home, remote := filepath.Join(root, "repo"), filepath.Join(root, "home"), filepath.Join(root, "remote.git")
			for _, dir := range []string{repo, home} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", home)
			t.Setenv("NSTACK_REVIEW_TEST_SCENARIO", name)
			config := filepath.Join(home, "config.toml")
			const original = "[mcp_servers.\"external.tool\"]\ncommand = \"false\"\n"
			fixtureWrite(config, original)
			git := func(args ...string) string {
				t.Helper()
				return gitTest(t, repo, args...)
			}
			git("init", "-qb", "main")
			git("config", "user.name", "Fixture")
			git("config", "user.email", "fixture@example.invalid")
			git("config", "commit.gpgsign", "false")
			fixtureWrite(filepath.Join(repo, "seed"), "base\n")
			git("add", "seed")
			git("commit", "-qm", "base")
			base := git("rev-parse", "HEAD")
			git("clone", "-q", "--bare", "--no-local", repo, remote)
			git("remote", "add", "origin", remote)
			git("switch", "-qc", "feature")
			readOnlyPass := c.mode == only || slices.Contains([]string{"review-first-pass", "review-first-detached", "review-first-fast-forward", "review-first-diverged"}, name)
			if c.mode != writer {
				value := "incorrect\n"
				if readOnlyPass {
					value = "correct\n"
				}
				fixtureWrite(filepath.Join(repo, "result.txt"), value)
				git("add", "result.txt")
				git("commit", "-qm", "existing candidate")
			}
			initial := git("rev-parse", "HEAD")
			deliveryRef, workRef := "refs/heads/feature", "refs/heads/authorized-work"
			if name == "delivery-none" {
				deliveryRef = "refs/heads/None"
			}
			if name == "work-none" {
				workRef = "refs/heads/None"
			}
			remoteGit := func(args ...string) string {
				t.Helper()
				return gitTest(t, remote, args...)
			}
			if branch, ok := strings.CutPrefix(name, "protected-delivery-"); ok {
				deliveryRef = "refs/heads/" + branch
				remoteGit("update-ref", "refs/heads/develop/v0.3.0", base)
				remoteGit("symbolic-ref", "HEAD", "refs/heads/develop/v0.3.0")
				remoteGit("update-ref", "-d", "refs/heads/main")
			}
			suffix := "refs/heads/archive/" + deliveryRef
			switch name {
			case "review-first-detached":
				git("switch", "--detach", initial)
			case "review-first-collision":
				git("branch", "authorized-work")
			case "remote-default":
				remoteGit("update-ref", deliveryRef, base)
				remoteGit("symbolic-ref", "HEAD", deliveryRef)
			case "delivery-mismatch", "review-only-fast-forward", "review-first-fast-forward", "delivery-ref-suffix":
				remoteGit("update-ref", deliveryRef, base)
				if name == "delivery-ref-suffix" {
					remoteGit("update-ref", suffix, base)
				}
			case "review-first-remediation-fast-forward":
				git("push", remote, initial+":"+deliveryRef)
			case "review-first-diverged":
				other := git("commit-tree", "HEAD^{tree}", "-p", base, "-m", "divergent remote tip")
				git("push", remote, other+":"+deliveryRef)
			case "dirty":
				fixtureWrite(filepath.Join(repo, "user-file"), "preserve this file\n")
			case "success":
				git("config", "push.followTags", "true")
				git("tag", "-am", "unapproved tag", "unapproved", base)
			case "detached-default":
				remoteGit("update-ref", "--no-deref", "HEAD", base)
				remoteGit("update-ref", "refs/remotes/origin/main", base)
				remoteGit("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
			case "assume-unchanged", "skip-worktree":
				git("update-index", "--"+name, "seed")
				fixtureWrite(filepath.Join(repo, "seed"), "concealed modification\n")
			}
			dest := remote
			if name == "rewritten" {
				dest = filepath.Join(root, "effective.git")
				git("clone", "-q", "--bare", remote, dest)
				git("config", "url."+dest+".pushInsteadOf", remote)
			}
			refValue := func(dir, ref string) string {
				return gitTest(t, dir, "for-each-ref", "--format=%(objectname)", ref)
			}
			remoteStart := refValue(dest, deliveryRef)
			evidence, brief := filepath.Join(root, "evidence"), filepath.Join(root, "brief")
			fixtureWrite(brief, "The result.txt output must be correct.\n")
			args := []string{"--authorize-host", "--repo", repo, "--base", base, "--brief", brief, "--evidence", evidence}
			if c.mode != "" {
				args = append(args, c.mode)
			}
			switch name {
			case "review-first-zero", "budget":
				args = append(args, "--max-remediations", "0")
			}
			if name != "review-first-detached" && name != "review-first-no-auth" && name != "local-no-auth" {
				destination := remote
				if name == "destination-changed" {
					destination = "origin"
				}
				args = append(args, "--push", destination, "--delivery-ref", deliveryRef)
				if c.mode != only && name != "review-first-pass" {
					args = append(args, "--work-ref", workRef)
				}
			}
			gateCode := "0"
			if name == "gate" || name == "review-first-gate" {
				gateCode = "7"
			}
			args = append(args, "--", "/bin/sh", "-c", `
read -r value < seed && [ "$value" = base ] || exit 99
git rev-parse HEAD && pwd -P || exit 99
exit "$1"`, "gate", gateCode)
			ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			var out, stderr bytes.Buffer
			code := workflow.RunCLI(ctx, args, &out, &stderr, Defaults(), New)
			status := readEvidence(t, evidence, "status")
			implementations, _ := filepath.Glob(filepath.Join(evidence, "*-implementation.json"))
			reviews, _ := filepath.Glob(filepath.Join(evidence, "*-review.json"))
			if len(implementations) != c.writes || len(reviews) != c.reviews {
				t.Errorf("implementation/review counts %d/%d, want %d/%d", len(implementations), len(reviews), c.writes, c.reviews)
			}
			if fixtureRead(config) != original || git("rev-parse", "refs/heads/feature") != initial ||
				refValue(dest, "refs/tags") != "" {
				t.Fatal("config, original ref or remote tags changed")
			}
			workStatus := ""
			if name == "dirty" {
				workStatus = "?? user-file"
			}
			if name == "assume-unchanged" || name == "skip-worktree" {
				if fixtureRead(filepath.Join(repo, "seed")) != "concealed modification\n" {
					t.Fatal("concealed file changed")
				}
				if _, err := os.Stat(filepath.Join(evidence, "request.json")); !os.IsNotExist(err) {
					t.Fatalf("workflow started with hidden index flags: %v", err)
				}
			}
			if git("status", "--porcelain", "--untracked-files=all") != workStatus {
				t.Fatal("unexpected checkout changes")
			}
			head := git("rev-parse", "HEAD")
			if c.writes == 0 && head != initial {
				t.Fatal("read-only path changed HEAD")
			}
			observedGate := func(gate object, expected any) string {
				t.Helper()
				sha, cwd, ok := strings.Cut(strings.TrimSuffix(fmt.Sprint(gate["stdout"]), "\n"), "\n")
				if !ok || sha != expected || !filepath.IsAbs(cwd) || cwd == repo {
					t.Fatalf("gate ran on %s at %s, want candidate %s in a disposable checkout", sha, cwd, expected)
				}
				return cwd
			}
			seen := map[any]bool{}
			for _, path := range append(implementations, reviews...) {
				name := strings.TrimSuffix(filepath.Base(path), ".json")
				record := readEvidence(t, evidence, name)
				if record["base"] != base {
					t.Errorf("wrong evidence base in %s: %v", name, record)
				}
				if session := record["session"]; session != nil && session != "" {
					model := "gpt-6.2-sol"
					if strings.HasSuffix(name, "-review") {
						model = "gpt-6.2-astra"
					}
					if seen[session] || record["model"] != model || record["effort"] != "medium" {
						t.Errorf("wrong role or reused session in %s: %v", name, record)
					}
					seen[session] = true
				}
				if n, ok := strings.CutSuffix(name, "-review"); ok {
					gate := readEvidence(t, evidence, n+"-gate")
					if record["candidate"] != gate["candidate"] || gate["base"] != base || gate["exitCode"] != float64(0) {
						t.Errorf("review not bound to passing gate: %v, %v", record, gate)
					}
					gateDir := observedGate(gate, record["candidate"])
					if c.mode != writer && n == "0" && record["candidate"] != initial {
						t.Errorf("first gate and review must examine the starting candidate %s", initial)
					}
					if verdict, ok := record["review"].(object); ok {
						reviewLog := fixtureRead(filepath.Join(evidence, name+".stderr"))
						_, checkout, found := strings.Cut(reviewLog, "fixture review checkout:\n")
						reviewSHA, reviewDir, observed := strings.Cut(strings.TrimSuffix(checkout, "\n"), "\n")
						if verdict["candidate"] != record["candidate"] || reviewSHA != verdict["candidate"] ||
							!found || !observed || !filepath.IsAbs(reviewDir) || reviewDir == repo || reviewDir == gateDir {
							t.Fatalf("gate and review not observed in separate candidate checkouts: gate=%q review=%q record=%v", gateDir, reviewLog, record)
						}
					}
				}
			}
			if c.error != "" {
				wantStatus := "blocked"
				if name == "review-only-changes" {
					wantStatus = "changes_requested"
					if status["candidate"] != initial {
						t.Errorf("wrong changes-requested candidate: %v", status)
					}
				}
				if code != 1 || out.Len() != 0 || status["status"] != wantStatus ||
					!strings.Contains(stderr.String(), c.error) || refValue(dest, deliveryRef) != remoteStart {
					t.Fatalf("want %q; exit=%d stdout=%s stderr=%s status=%v", c.error, code, &out, &stderr, status)
				}
				if name == "gate" || name == "review-first-gate" {
					gate := readEvidence(t, evidence, "0-gate")
					if gate["exitCode"] != float64(7) || gate["candidate"] != head {
						t.Errorf("wrong failure evidence: %v", gate)
					}
					observedGate(gate, head)
				}
				if name == "review-first-repeat" {
					first := readEvidence(t, evidence, "0-review")
					second := readEvidence(t, evidence, "1-review")
					ids := first["newFindingIds"].(object)
					prior := second["review"].(object)["previous"].([]any)
					if len(ids) != 2 || ids["F1"] == nil || ids["F2"] == nil || len(prior) != 2 || prior[0].(object)["id"] != "F2" ||
						prior[1].(object)["id"] != "F1" || prior[1].(object)["status"] != "recurring" ||
						second["candidate"] != head || first["candidate"] == head {
						t.Fatalf("unexpected finding lineage: %v / %v", first, second)
					}
				}
				if name == "review-first-unknown" || name == "review-first-missing" {
					record := readEvidence(t, evidence, "1-review")
					if record["candidate"] != head || record["message"] == "" || record["validationError"] == nil {
						t.Fatalf("missing invalid attempt evidence: %v", record)
					}
				}
				return
			}
			if code != 0 || stderr.Len() != 0 || status["status"] != "pass" || status["candidate"] != head ||
				!strings.Contains(out.String(), head) || !strings.Contains(out.String(), evidence) {
				t.Fatalf("exit=%d stdout=%s stderr=%s status=%v", code, &out, &stderr, status)
			}
			preflight := readEvidence(t, evidence, "preflight")
			if preflight["disabledMcpServers"] != float64(1) || preflight["gateScratchWrite"] != true {
				t.Fatalf("missing preflight checks: %v", preflight)
			}
			last := readEvidence(t, evidence, strconv.Itoa(c.reviews-1)+"-review")
			if last["writeProbe"] != "write-denied 13" || last["review"].(object)["verdict"] != "pass" {
				t.Fatalf("missing final review evidence: %v", last)
			}
			if !readOnlyPass && (git("symbolic-ref", "HEAD") != workRef || fixtureRead(filepath.Join(repo, "result.txt")) != "correct\n") {
				t.Fatal("remediation did not reach the authorized writer output")
			}
			if name == "review-first-detached" {
				if refValue(dest, deliveryRef) != "" {
					t.Fatal("unexpected remote write")
				}
			} else if refValue(dest, deliveryRef) != head {
				t.Fatal("did not deliver exact passing candidate")
			}
			if name == "delivery-ref-suffix" && refValue(remote, suffix) != base || name == "rewritten" && refValue(remote, deliveryRef) != "" {
				t.Fatal("unrelated remote ref changed")
			}
		})
	}
}
