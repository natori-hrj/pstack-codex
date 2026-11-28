package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func (f checkoutFixture) record(t *testing.T, name string) Evidence {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.evidence, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var value Evidence
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func fixtureGit(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null"}, argv...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v: %s", argv, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixtureFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func localGitOnly(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
}

func TestFixtureWorktreesKeepIndependentChanges(t *testing.T) {
	localGitOnly(t)
	f := testCheckout(t)
	fixtureFile(t, filepath.Join(f.directory, "value"), "incorrect\n")
	fixtureGit(t, f.directory, "add", "value")
	fixtureGit(t, f.directory, "commit", "-qm", "reproducible fixture")
	head := fixtureGit(t, f.directory, "rev-parse", "HEAD")
	root := t.TempDir()
	fix, docs, review := filepath.Join(root, "fix"), filepath.Join(root, "docs"), filepath.Join(root, "review")
	for _, dir := range []string{fix, docs, review} {
		fixtureGit(t, f.directory, "worktree", "add", "--detach", dir, head)
	}
	check := func(dir string, want bool) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "value"))
		if err != nil || (string(data) == "correct\n") != want {
			t.Fatalf("%s: contract check=%q, %v", dir, data, err)
		}
	}
	check(review, false)
	fixtureFile(t, filepath.Join(fix, "value"), "correct\n")
	check(fix, true)
	fixtureFile(t, filepath.Join(docs, "README.md"), "The correct result is required.\n")
	for dir, expected := range map[string]string{fix: "M value", docs: "?? README.md", review: "", f.directory: ""} {
		if status := fixtureGit(t, dir, "status", "--porcelain"); status != expected ||
			fixtureGit(t, dir, "rev-parse", "HEAD") != head {
			t.Fatalf("changes escaped the intended worktree %s: %q", dir, status)
		}
	}
	check(review, false)
	if fixtureGit(t, f.directory, "remote") != "" {
		t.Fatal("fixture acquired a remote")
	}
}

// TestPortableReviewContract ports the published v0.3.0 portable cases. Git
// SHA values are read from the fixture checkout; verdicts/status are fixed.
func TestPortableReviewContract(t *testing.T) {
	localGitOnly(t)
	for _, c := range []struct {
		name, flag, status, diagnostic string
		code, gateCode                 int
	}{
		{"unauthorized", "--review-only", "", "--authorize-host", 2, 0},
		{"pass", "--review-only", "pass", "", 0, 0},
		{"review-first", "--review-first", "pass", "", 0, 0},
		{"implementation", "--work-ref", "pass", "", 0, 0},
		{"changes-requested", "--review-only", "changes_requested", "requested changes", 1, 0},
		{"wrong-sha", "--review-only", "blocked", "does not match the candidate", 1, 0},
		{"gate-failed", "--review-only", "blocked", "quality gate failed", 1, 7},
		{"missing-capability", "--review-only", "blocked", "required read-only capability", 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := testCheckout(t)
			mode := []string{c.flag}
			workRef := "refs/heads/fixture-work"
			writer := c.name == "implementation"
			if writer {
				mode = append(mode, workRef)
			}
			args := f.args(mode...)
			if c.name == "unauthorized" {
				args = args[1:]
			}
			wantValue := ""
			if writer {
				wantValue = "implemented\n"
			}
			adapter := portableFixture{testAdapter{
				preflight: func(context.Context) (Evidence, error) {
					if c.name == "missing-capability" {
						return nil, errors.New("required read-only capability unavailable")
					}
					return Evidence{"readOnly": true}, nil
				},
				gate: func() GateResult { return GateResult{ExitCode: &c.gateCode} },
				run: func(_ context.Context, req TurnRequest) (TurnResult, error) {
					result := TurnResult{Model: req.Role.Model, Effort: req.Role.Effort, Session: "independent-review"}
					if req.Schema == nil {
						fixtureFile(t, filepath.Join(req.Directory, "value.txt"), wantValue)
						result.Session, result.Message = "implementation", "updated"
						return result, nil
					}
					sha := fixtureGit(t, req.Directory, "rev-parse", "HEAD")
					verdict := Evidence{"candidate": sha, "verdict": "pass", "findings": []Evidence{}}
					switch c.name {
					case "wrong-sha":
						verdict["candidate"] = strings.Repeat("0", 40)
					case "changes-requested":
						verdict["verdict"] = "changes_requested"
						verdict["findings"] = []Evidence{{"location": "value.txt", "evidence": "actual", "expected": "correct", "impact": "incorrect output"}}
					}
					if c.flag != "--review-only" {
						verdict["previous"] = []Evidence{}
					}
					raw, err := json.Marshal(verdict)
					result.Message = string(raw)
					return result, err
				},
			}}
			var out, stderr bytes.Buffer
			code := RunCLI(context.Background(), args, &out, &stderr, contractRoles,
				func(time.Duration) (Adapter, error) { return adapter, nil })
			if code != c.code || !strings.Contains(stderr.String(), c.diagnostic) ||
				(code != 0 && out.Len() != 0) || (code == 0 && stderr.Len() != 0) {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, &out, &stderr)
			}
			if fixtureGit(t, f.directory, "status", "--porcelain") != "" ||
				fixtureGit(t, f.directory, "rev-parse", "refs/heads/fixture") != f.head {
				t.Fatal("fixture checkout or original ref changed")
			}
			head := fixtureGit(t, f.directory, "rev-parse", "HEAD")
			refs := fixtureGit(t, f.directory, "show-ref")
			wantRefs := f.head + " refs/heads/fixture"
			if writer {
				wantRefs += "\n" + head + " " + workRef
				data, err := os.ReadFile(filepath.Join(f.directory, "value.txt"))
				if err != nil || string(data) != wantValue || head == f.head ||
					fixtureGit(t, f.directory, "rev-parse", "HEAD^") != f.head ||
					fixtureGit(t, f.directory, "symbolic-ref", "HEAD") != workRef {
					t.Fatalf("unexpected writer result: %q, %v", data, err)
				}
			} else if head != f.head || fixtureGit(t, f.directory, "symbolic-ref", "HEAD") != "refs/heads/fixture" {
				t.Fatal("review changed HEAD")
			}
			if refs != wantRefs {
				t.Fatalf("unexpected refs: %s", refs)
			}
			absent := func(path string) {
				t.Helper()
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s should be absent: %v", path, err)
				}
			}
			if c.name == "unauthorized" {
				absent(f.evidence)
				return
			}
			status, request := f.record(t, "status"), f.record(t, "request")
			if status["status"] != c.status || c.status != "blocked" && status["candidate"] != head ||
				request["adapter"] != "portable-fixture" || request["start"] != f.head || request["base"] != f.base ||
				!reflect.DeepEqual(request["gate"], []any{"fixture-gate"}) ||
				request["reviewOnly"] != (c.flag == "--review-only") || request["reviewFirst"] != (c.flag == "--review-first") ||
				request["maxRemediations"] != float64(2) {
				t.Fatalf("unexpected request/status: %v / %v", request, status)
			}
			absent(filepath.Join(f.evidence, "delivery.json"))
			if !writer {
				absent(filepath.Join(f.evidence, "0-implementation.json"))
			} else {
				record := f.record(t, "0-implementation")
				if record["start"] != f.head || record["base"] != f.base || record["workRef"] != workRef ||
					record["model"] != "gpt-6.2-sol" || record["effort"] != "medium" || record["session"] != "implementation" {
					t.Fatalf("unexpected implementation evidence: %v", record)
				}
			}
			if c.name == "missing-capability" {
				absent(filepath.Join(f.evidence, "0-gate.json"))
			} else {
				gate := f.record(t, "0-gate")
				if gate["candidate"] != head || gate["base"] != f.base || gate["exitCode"] != float64(c.gateCode) {
					t.Fatalf("unexpected gate evidence: %v", gate)
				}
			}
			if c.name == "missing-capability" || c.gateCode != 0 {
				absent(filepath.Join(f.evidence, "0-review.json"))
				return
			}
			review := f.record(t, "0-review")
			if review["candidate"] != head || review["base"] != f.base || review["session"] != "independent-review" ||
				review["model"] != "gpt-6.2-astra" || review["effort"] != "medium" {
				t.Fatalf("unexpected review evidence: %v", review)
			}
			if c.name == "wrong-sha" {
				if review["review"] != nil || !strings.Contains(fmt.Sprint(review["validationError"]), c.diagnostic) {
					t.Fatalf("wrong SHA accepted: %v", review)
				}
				return
			}
			var message map[string]any
			err := json.Unmarshal([]byte(review["message"].(string)), &message)
			if err != nil || !reflect.DeepEqual(message, review["review"]) || message["candidate"] != head || message["verdict"] != c.status {
				t.Fatalf("unexpected accepted verdict: %v, %v", review, err)
			}
			findings := message["findings"].([]any)
			if c.status == "changes_requested" {
				if !reflect.DeepEqual(findings, []any{map[string]any{"location": "value.txt", "evidence": "actual", "expected": "correct", "impact": "incorrect output"}}) {
					t.Fatalf("wrong findings: %v", findings)
				}
			} else if len(findings) != 0 {
				t.Fatalf("unexpected findings: %v", findings)
			}
			if c.flag != "--review-only" && !reflect.DeepEqual(message["previous"], []any{}) {
				t.Fatalf("wrong prior findings: %v", message)
			}
		})
	}
}

type portableFixture struct{ testAdapter }

func (portableFixture) Name() string { return "portable-fixture" }

var contractRoles = RoleDefaults{
	Writer:   Role{Model: "gpt-6.2-sol", Effort: "medium"},
	Reviewer: Role{Model: "gpt-6.2-astra", Effort: "medium"},
}

const fixtureTarget = "https://github.com/fixture/project"

func TestMain(m *testing.M) {
	if os.Getenv("NSTACK_PR_FIXTURE") != "" {
		args := os.Args[1:]
		switch filepath.Base(os.Args[0]) {
		case "git":
			path := os.Getenv("NSTACK_REAL_GIT")
			if slices.Contains(args, "ls-remote") || slices.Contains(args, "push") {
				for i, value := range args {
					if strings.HasPrefix(value, "remote.nstack-delivery-") && strings.HasSuffix(value, "="+fixtureTarget) {
						args[i] = strings.TrimSuffix(value, fixtureTarget) + os.Getenv("NSTACK_FIXTURE_REMOTE")
					}
				}
			}
			panic(syscall.Exec(path, append([]string{path}, args...), os.Environ()))
		case "gh":
			os.Exit(fixtureGH(args))
		}
	}
	os.Exit(m.Run())
}

func fixtureGH(args []string) int {
	assert := func(ok bool) {
		if !ok {
			panic(fmt.Sprint("unexpected gh fixture call: ", args))
		}
	}
	scenario, state, remote := os.Getenv("NSTACK_PR_FIXTURE"), os.Getenv("NSTACK_FIXTURE_STATE"), os.Getenv("NSTACK_FIXTURE_REMOTE")
	assert(len(args) >= 4 && args[0] == "pr" && slices.Equal(args[len(args)-2:], []string{"--repo", "github.com/fixture/project"}))
	action, flags := args[1], args[2:len(args)-2]
	marker := func(name string) bool {
		_, err := os.Stat(filepath.Join(state, name))
		return err == nil
	}
	mark := func(name string) { assert(os.WriteFile(filepath.Join(state, name), nil, 0600) == nil) }
	git := func(argv ...string) string {
		cmd := exec.Command(os.Getenv("NSTACK_REAL_GIT"), append([]string{"-C", remote}, argv...)...)
		out, err := cmd.Output()
		assert(err == nil)
		return strings.TrimSpace(string(out))
	}
	emit := func(value any) { assert(json.NewEncoder(os.Stdout).Encode(value) == nil) }
	existing := scenario != "new" && scenario != "post-push-failure"
	switch action {
	case "list":
		assert(slices.Equal(flags, []string{"--state", "open", "--head", "feature", "--limit", "100", "--json", "number,isCrossRepository"}))
		rows := []Evidence{{"number": 90, "isCrossRepository": true}}
		if existing || marker("created") {
			rows = append(rows, Evidence{"number": 42, "isCrossRepository": false})
		}
		if scenario == "ambiguous" || scenario == "explicit-ambiguous" || scenario == "newly-ambiguous" && marker("viewed") {
			rows = append(rows, Evidence{"number": 43, "isCrossRepository": false})
		}
		emit(rows)
	case "create":
		assert(!existing && !marker("created"))
		assert(slices.Equal(flags, []string{"--draft", "--head", "feature", "--base", "main", "--title", "Fixture", "--body", ""}))
		if scenario == "post-push-failure" {
			return 1
		}
		mark("created")
		fmt.Println(fixtureTarget + "/pull/42")
	case "view":
		assert(slices.Equal(flags, []string{"42", "--json", "number,url,state,headRefName,headRefOid,baseRefName,baseRefOid,isCrossRepository"}))
		tip := git("rev-parse", "refs/heads/feature")
		base := os.Getenv("NSTACK_FIXTURE_BASE")
		candidate := os.Getenv("NSTACK_FIXTURE_CANDIDATE")
		if scenario == "remote-moved" && !marker("viewed") {
			git("update-ref", "refs/heads/feature", candidate, tip)
		}
		mark("viewed")
		head := "feature"
		if scenario == "wrong-ref" {
			head = "wrong"
		}
		if scenario == "wrong-base" {
			base = candidate
		}
		emit(Evidence{"number": 42, "url": fixtureTarget + "/pull/42", "state": "OPEN", "isCrossRepository": false,
			"headRefName": head, "headRefOid": tip, "baseRefName": "main", "baseRefOid": base})
	default:
		assert(false)
	}
	return 0
}

func TestPRDeliveryContract(t *testing.T) {
	localGitOnly(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gh", "git"} {
		if err := os.Symlink(executable, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ name, error string }{
		{"new", ""}, {"existing", ""}, {"ambiguous", "unique open PR"}, {"wrong-base", "reviewed base"},
		{"wrong-ref", "authorized branches"}, {"explicit", ""}, {"explicit-other", "authorized PR"},
		{"explicit-ambiguous", "unique open PR"}, {"newly-ambiguous", "unique open PR"},
		{"remote-moved", "delivery candidate changed"}, {"post-push-failure", "gh pr create"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := testCheckout(t)
			root := t.TempDir()
			remote := filepath.Join(root, "remote.git")
			vcs := func(args ...string) string { return fixtureGit(t, f.directory, args...) }
			vcs("branch", "main", f.base)
			vcs("clone", "-q", "--bare", "--no-local", f.directory, remote)
			vcs("-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
			initial := ""
			if c.name != "new" && c.name != "post-push-failure" {
				initial = f.base
				vcs("-C", remote, "update-ref", "refs/heads/feature", initial)
			}
			vcs("remote", "add", "delivery", fixtureTarget)
			t.Setenv("NSTACK_REAL_GIT", git)
			t.Setenv("NSTACK_PR_FIXTURE", c.name)
			t.Setenv("NSTACK_FIXTURE_STATE", root)
			t.Setenv("NSTACK_FIXTURE_REMOTE", remote)
			t.Setenv("NSTACK_FIXTURE_BASE", f.base)
			t.Setenv("NSTACK_FIXTURE_CANDIDATE", f.head)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			flags := []string{"--review-only", "--push", "delivery", "--delivery-ref", "refs/heads/feature",
				"--github-repo", "fixture/project", "--pr-base", "main"}
			switch c.name {
			case "explicit", "explicit-ambiguous", "newly-ambiguous":
				flags = append(flags, "--pr-number", "42")
			case "explicit-other":
				flags = append(flags, "--pr-number", "99")
			default:
				flags = append(flags, "--pr-title", "Fixture")
			}
			adapter := testAdapter{run: func(_ context.Context, req TurnRequest) (TurnResult, error) {
				sha := fixtureGit(t, req.Directory, "rev-parse", "HEAD")
				return TurnResult{Session: "pr-review", Model: req.Role.Model, Effort: req.Role.Effort,
					Message: fmt.Sprintf(`{"candidate":%q,"verdict":"pass","findings":[]}`, sha)}, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var out, stderr bytes.Buffer
			code := runFixtureCLI(ctx, f.args(flags...), &out, &stderr, func(time.Duration) (Adapter, error) { return adapter, nil })
			expected := initial
			if c.error == "" {
				status := f.record(t, "status")
				if code != 0 || status["status"] != "pass" || status["candidate"] != f.head {
					t.Fatalf("exit=%d stderr=%s status=%v", code, &stderr, status)
				}
				want := Evidence{"candidate": f.head, "base": f.base, "ref": "refs/heads/feature",
					"defaultRef": "refs/heads/main", "pr": fixtureTarget + "/pull/42"}
				if record := f.record(t, "delivery"); !reflect.DeepEqual(record, want) {
					t.Fatalf("delivery=%v want=%v", record, want)
				}
				expected = f.head
			} else {
				if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), c.error) {
					t.Fatalf("want %q; exit=%d stdout=%s stderr=%s", c.error, code, &out, &stderr)
				}
				f.blocked(t, c.error)
				if _, err := os.Stat(filepath.Join(f.evidence, "delivery.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected delivery evidence: %v", err)
				}
				if c.name == "post-push-failure" || c.name == "remote-moved" {
					expected = f.head
				}
			}
			if tip := vcs("-C", remote, "for-each-ref", "--format=%(objectname)", "refs/heads/feature"); tip != expected {
				t.Fatalf("remote feature=%q want=%q", tip, expected)
			}
			_, err := os.Stat(filepath.Join(root, "created"))
			if (err == nil) != (c.name == "new") || vcs("rev-parse", "HEAD") != f.head ||
				vcs("symbolic-ref", "HEAD") != "refs/heads/fixture" || vcs("status", "--porcelain") != "" ||
				vcs("-C", remote, "rev-parse", "refs/heads/main") != f.base {
				t.Fatal("unexpected PR, checkout or base side effect")
			}
		})
	}
}
