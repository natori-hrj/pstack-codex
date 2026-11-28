package codex

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func installProtocolDouble(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// TestMain dispatches only the test binary installed as the fixture executable.
// The double simulates protocol claims; native_test.go probes real enforcement.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == lifecycleFixtureArgument {
		if err := runLifecycleFixture(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == SupervisorArgument {
		if err := Supervise(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if filepath.Base(os.Args[0]) == "codex" {
		runProtocolDouble()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureCheck(ok bool, msg string, args ...any) {
	if !ok {
		panic(fmt.Sprintf(msg, args...))
	}
}

func fixtureRead(path string) string {
	data, err := os.ReadFile(path)
	fixtureCheck(err == nil, "read %s: %v", path, err)
	return string(data)
}

func fixtureWrite(path, text string) {
	err := os.WriteFile(path, []byte(text), 0600)
	fixtureCheck(err == nil, "write %s: %v", path, err)
}

func runProtocolDouble() {
	scenario := os.Getenv("NSTACK_REVIEW_TEST_SCENARIO")
	fixtureCheck(os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("CODEX_API_KEY") == "", "API key inherited")
	fixtureCheck(os.Getenv("PYTHONDONTWRITEBYTECODE") == "1" &&
		slices.Contains(os.Args, `shell_environment_policy.set.PYTHONDONTWRITEBYTECODE="1"`), "bytecode guard missing")
	cwd, err := os.Getwd()
	fixtureCheck(err == nil, "cwd: %v", err)
	var config struct {
		Servers map[string]object `toml:"mcp_servers"`
	}
	_, err = toml.Decode(fixtureRead(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")), &config)
	fixtureCheck(err == nil, "decode config: %v", err)
	features := object{}
	for i, arg := range os.Args {
		if arg == "--disable" {
			features[os.Args[i+1]] = false
		}
		if strings.HasPrefix(arg, "mcp_servers=") && scenario != "mcp-forced" {
			var overrides struct {
				Servers map[string]object `toml:"mcp_servers"`
			}
			_, err := toml.Decode(arg, &overrides)
			fixtureCheck(err == nil, "decode MCP override: %v", err)
			for name, values := range overrides.Servers {
				for key, value := range values {
					config.Servers[name][key] = value
				}
			}
		}
	}
	id := make([]byte, 16)
	_, err = rand.Read(id)
	fixtureCheck(err == nil, "thread identity: %v", err)
	thread := hex.EncodeToString(id)
	var state object
	send := func(value object) {
		err := json.NewEncoder(os.Stdout).Encode(value)
		fixtureCheck(err == nil, "emit: %v", err)
	}
	notify := func(method string, params object) {
		params["threadId"], params["turnId"] = thread, "turn"
		send(object{"method": method, "params": params})
	}
	lines := bufio.NewScanner(os.Stdin)
	lines.Buffer(make([]byte, 64<<10), 8<<20)
	for lines.Scan() {
		var req struct {
			ID     *int
			Method string
			Params object
		}
		err := json.Unmarshal(lines.Bytes(), &req)
		fixtureCheck(err == nil, "decode request: %v", err)
		if req.ID == nil {
			continue
		}
		if req.Method != "initialize" && req.Method != "config/read" {
			for name, server := range config.Servers {
				fixtureCheck(server["enabled"] == false, "MCP still enabled at dispatch: %s", name)
			}
		}
		p := req.Params
		reply := func(result object) { send(object{"id": *req.ID, "result": result}) }
		switch req.Method {
		case "initialize":
			switch scenario {
			case "unexpected-id":
				send(object{"id": *req.ID + 1, "result": object{}})
			case "missing-response":
				send(object{"id": *req.ID})
			case "invalid-json":
				fmt.Println(`{"id":1,"id":1,"result":{}}`)
			case "partial-frame":
				fmt.Print(`{"id":1,"result":{}}`)
				return
			default:
				reply(object{})
			}
		case "config/read":
			reply(object{"config": object{"mcp_servers": config.Servers, "features": features, "web_search": "disabled"}})
		case "account/read":
			var account any = object{"type": "chatgpt"}
			switch scenario {
			case "unauthenticated":
				account = nil
			case "api-key-account":
				account = object{"type": "apiKey"}
			case "account-case-alias":
				account = object{"type": "apiKey", "TYPE": "chatgpt"}
			}
			reply(object{"account": account})
		case "model/list":
			fixtureCheck(p["modelProvider"] == "openai", "model provider: %v", p)
			model, cursor := "gpt-6.2-sol", any("page-2")
			second := p["cursor"] != nil
			if second {
				fixtureCheck(p["cursor"] == "page-2", "unexpected cursor: %v", p)
				model, cursor = "gpt-6.2-astra", nil
			}
			rows := []object{}
			if !(scenario == "model" && second || scenario == "review-first-pass" && !second) {
				rows = append(rows, object{"model": model, "supportedReasoningEfforts": []object{{"reasoningEffort": "medium"}}})
			}
			reply(object{"data": rows, "nextCursor": cursor})
		case "command/exec":
			reply(fixtureCommand(p, scenario))
		case "thread/start":
			state = p
			reply(fixtureThread(p, cwd, thread, scenario))
		case "turn/start":
			switch scenario {
			case "turn-eof", "turn-hang":
				child := exec.Command("/bin/sleep", "30")
				pidFile := os.Getenv("NSTACK_TEST_CHILD_PID")
				child.Args[0] = pidFile // Unique argv[0] for emergency test cleanup.
				child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				err := child.Start()
				fixtureCheck(err == nil, "start child: %v", err)
				fixtureWrite(pidFile, fmt.Sprint(child.Process.Pid))
				if scenario == "turn-eof" {
					for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
						if _, err := os.Stat(pidFile + ".exit"); err == nil {
							break
						}
						time.Sleep(5 * time.Millisecond)
					}
					os.Exit(7)
				}
				time.Sleep(30 * time.Second)
				return
			case "missing-turn":
				reply(object{"turn": object{}})
				continue
			}
			turn := object{"turn": object{"id": "turn"}}
			reply(turn)
			message := fixtureMessage(state, p, cwd, scenario)
			if scenario == "reroute" && p["outputSchema"] != nil {
				notify("model/rerouted", object{"toModel": "another-model"})
			}
			notify("item/completed", object{"item": object{"type": "agentMessage", "text": message}})
			notify("thread/tokenUsage/updated", object{"tokenUsage": object{"total": object{"inputTokens": 10}}})
			terminal := object{"id": "turn", "status": "completed", "error": nil}
			if scenario == "missing-terminal-error" {
				delete(terminal, "error")
			}
			notify("turn/completed", object{"turn": terminal})
			if scenario == "late-verdict" {
				notify("item/completed", object{"item": object{"type": "agentMessage", "text": message}})
			}
			if scenario == "duplicate-turn-response" {
				reply(turn)
			}
		default:
			panic("unexpected method: " + req.Method)
		}
	}
	fixtureCheck(lines.Err() == nil, "read protocol: %v", lines.Err())
}

func fixtureCommand(p object, scenario string) object {
	policy := p["sandboxPolicy"].(object)
	fixtureCheck(policy["networkAccess"] == false, "network allowed: %v", policy)
	var argv []string
	for _, arg := range p["command"].([]any) {
		argv = append(argv, arg.(string))
	}
	if policy["type"] == "readOnly" {
		if scenario == "permission" {
			fixtureWrite(argv[len(argv)-1], "")
		}
		return object{"exitCode": 0, "stdout": "write-denied 13\n", "stderr": ""}
	}
	env := p["env"].(object)
	fixtureCheck(policy["excludeSlashTmp"] == true && policy["excludeTmpdirEnvVar"] == true &&
		slices.Contains(policy["writableRoots"].([]any), env["TMPDIR"]), "invalid gate policy: %v", p)
	if scenario == "gate-scratch" {
		return object{"exitCode": 1, "stdout": "", "stderr": ""}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = p["cwd"].(string)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value.(string))
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		fixtureCheck(ok, "run fixture gate: %v", err)
		code = exit.ExitCode()
	}
	return object{"exitCode": code, "stdout": string(stdout), "stderr": stderr.String()}
}

func fixtureThread(p object, cwd, thread, scenario string) object {
	fixtureCheck(p["cwd"] == cwd && p["ephemeral"] == true && p["approvalPolicy"] == "never" &&
		p["modelProvider"] == "openai" && p["config"].(object)["model_reasoning_effort"] == "medium",
		"unexpected thread request: %v", p)
	sandbox := object{"type": "readOnly", "networkAccess": false}
	if p["sandbox"] == "workspace-write" {
		sandbox = object{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{cwd},
			"excludeTmpdirEnvVar": true, "excludeSlashTmp": true}
	}
	identity := object{"id": thread, "sessionId": thread, "forkedFromId": nil, "parentThreadId": nil, "ephemeral": true}
	result := object{"thread": identity, "cwd": cwd, "model": p["model"], "modelProvider": "openai",
		"reasoningEffort": "medium", "approvalPolicy": "never", "sandbox": sandbox}
	switch scenario {
	case "wrong-policy":
		if p["sandbox"] == "read-only" {
			sandbox["type"] = "workspaceWrite"
		}
	case "missing-session":
		delete(identity, "sessionId")
	case "missing-thread-lineage":
		delete(identity, "parentThreadId")
		delete(identity, "forkedFromId")
	case "missing-writer-roots":
		delete(sandbox, "writableRoots")
	case "wrong-effort":
		result["reasoningEffort"] = "low"
	case "wrong-model":
		result["model"] = "another-model"
	case "model-case-alias":
		result["model"], result["MODEL"] = "another-model", p["model"]
	}
	return result
}

func fixtureGit(cwd string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	fixtureCheck(err == nil, "git %v: %v: %s", args, err, out)
	return strings.TrimSpace(string(out))
}

func fixtureMessage(state, p object, cwd, scenario string) string {
	prompt := p["input"].([]any)[0].(object)["text"].(string)
	target := filepath.Join(cwd, "result.txt")
	if p["outputSchema"] == nil {
		fixtureCheck(state["model"] == "gpt-6.2-sol" && state["sandbox"] == "workspace-write",
			"wrong writer settings: %v", state)
		fixtureCheck(fixtureRead(filepath.Join(cwd, "seed")) == "base\n", "unexpected writer inputs")
		switch {
		case scenario == "writer-ref-changed":
			fixtureGit(cwd, "switch", "-qc", "unexpected")
		case scenario == "destination-changed":
			fixtureGit(cwd, "remote", "set-url", "origin", filepath.Join(cwd, "unexpected.git"))
		case strings.Contains(prompt, "Fix demonstrated findings"):
			fixtureCheck(strings.Contains(prompt, "incorrect output"), "missing finding in writer prompt")
			value := "correct\n"
			if scenario == "review-first-two-rounds" && fixtureRead(target) == value {
				value = "incorrect-two\n"
			}
			fixtureWrite(target, value)
		default:
			fixtureWrite(target, "incorrect\n")
		}
		return "implementation transcript marker"
	}
	fixtureCheck(state["model"] == "gpt-6.2-astra" && state["sandbox"] == "read-only" &&
		!strings.Contains(prompt, "implementation transcript marker"), "review context not isolated")
	sha := fixtureGit(cwd, "rev-parse", "HEAD")
	fixtureCheck(len(sha) == 40 && strings.Contains(prompt, sha), "wrong candidate: %s", prompt)
	fmt.Fprintf(os.Stderr, "fixture review checkout:\n%s\n%s\n", sha, cwd)
	_, tracked := p["outputSchema"].(object)["properties"].(object)["previous"]
	var registry map[string]object
	if tracked {
		_, raw, _ := strings.Cut(prompt, "Registry: ")
		err := json.Unmarshal([]byte(raw), &registry)
		fixtureCheck(err == nil, "review registry: %v", err)
	}
	if len(registry) > 0 && scenario == "review-first-repeat" {
		fixtureCheck(strings.Contains(registry["F1"]["initialEvidence"].(string), "input alpha") &&
			strings.Contains(registry["F2"]["initialEvidence"].(string), "input beta"), "registry reordered")
	}
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	previous := []object{}
	for _, id := range ids {
		previous = append(previous, object{"id": id, "status": "resolved", "evidence": "candidate " + sha + " checked at " + target})
	}
	verdict := "pass"
	if len(previous) > 0 {
		last := previous[len(previous)-1]
		switch scenario {
		case "repeat", "review-first-repeat":
			last["status"], verdict = "recurring", "changes_requested"
		case "review-first-uncertain":
			last["status"], verdict = "uncertain", "changes_requested"
		case "review-first-unknown":
			last["id"] = "unknown"
		case "review-first-missing":
			previous = previous[:len(previous)-1]
		}
	}
	findings := []object{}
	if fixtureRead(target) != "correct\n" || slices.Contains([]string{"review-only-changes", "review-first-two-rounds", "late-verdict"}, scenario) ||
		scenario == "review-first-repeat" && len(registry) > 0 {
		verdict = "changes_requested"
		finding := func(input string) object {
			return object{"location": target, "evidence": "incorrect output at " + sha + input,
				"expected": "correct output", "impact": "wrong user-facing result"}
		}
		if scenario == "review-first-repeat" && len(registry) == 0 {
			findings = append(findings, finding(" for input alpha"), finding(" for input beta"))
		} else {
			findings = append(findings, finding(""))
		}
	}
	if scenario == "review-first-blocked" {
		verdict = "blocked"
	}
	if scenario == "sha" {
		sha = strings.Repeat("0", 40)
	}
	result := object{"candidate": sha, "verdict": verdict, "findings": findings}
	if tracked {
		result["previous"] = previous
	}
	if scenario == "malformed-verdict" {
		return `{"verdict":`
	}
	jsonResult, err := json.Marshal(result)
	fixtureCheck(err == nil, "encode verdict: %v", err)
	return string(jsonResult)
}
