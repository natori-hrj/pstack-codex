package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/natori-hrj/nstack/internal/workflow"
)

type Adapter struct{ timeout time.Duration }

func New(timeout time.Duration) (workflow.Adapter, error) {
	if timeout <= 0 {
		return nil, errors.New("Codex timeout must be positive")
	}
	return &Adapter{timeout: timeout}, nil
}

func (*Adapter) Name() string { return "codex-app-server" }

type effectiveConfig struct {
	MCP      json.RawMessage  `json:"mcp_servers"`
	Features map[string]*bool `json:"features"`
	Search   string           `json:"web_search"`
}

func (s *server) config(cwd string) (effectiveConfig, []string, error) {
	var response struct {
		Config effectiveConfig `json:"config"`
	}
	if err := s.request("config/read", object{"cwd": cwd}, &response, nil); err != nil {
		return response.Config, nil, err
	}
	var servers map[string]struct {
		Enabled *bool `json:"enabled"`
	}
	if len(response.Config.MCP) == 0 || unmarshalWire(response.Config.MCP, &servers) != nil {
		return response.Config, nil, errors.New("Codex did not report effective MCP configuration")
	}
	var active []string
	for name, config := range servers {
		if config.Enabled == nil || *config.Enabled {
			active = append(active, name)
		}
	}
	sort.Strings(active)
	return response.Config, active, nil
}

func open(ctx context.Context, cwd, evidence string, requireOAuth bool) (*server, int, error) {
	s, err := startServer(ctx, cwd, evidence+"-config")
	if err != nil {
		return nil, 0, err
	}
	cfg, active, err := s.config(cwd)
	disabled := len(active)
	if err == nil && disabled != 0 {
		if err := s.close(); err != nil {
			return nil, 0, err
		}
		entries := make([]string, 0, disabled)
		for _, name := range active {
			quoted, err := json.Marshal(name)
			if err != nil {
				return nil, 0, err
			}
			entries = append(entries, string(quoted)+" = { enabled = false }")
		}
		s, err = startServer(ctx, cwd, evidence, "-c", "mcp_servers={ "+strings.Join(entries, ", ")+" }")
		if err != nil {
			return nil, 0, err
		}
		cfg, active, err = s.config(cwd)
	}
	if err == nil && len(active) != 0 {
		err = errors.New("Codex orchestration MCP overrides did not take effect")
	}
	if err == nil {
		err = verifyFeatures(cfg)
	}
	if err == nil && requireOAuth {
		err = s.verifyOAuth()
	}
	if err != nil {
		return nil, 0, errors.Join(err, s.close())
	}
	return s, disabled, nil
}

func verifyFeatures(cfg effectiveConfig) error {
	invalid := errors.New("Codex orchestration feature overrides did not take effect")
	if cfg.Search != "disabled" {
		return invalid
	}
	for _, name := range disabledFeatures {
		if value := cfg.Features[name]; value == nil || *value {
			return invalid
		}
	}
	return nil
}

func (s *server) verifyOAuth() error {
	var response struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := s.request("account/read", object{"refreshToken": false}, &response, nil); err != nil {
		return err
	}
	if response.Account == nil {
		return errors.New("Codex has no authenticated account; use normal Codex login on the host")
	}
	if response.Account.Type != "chatgpt" {
		return errors.New("Codex requires the existing ChatGPT OAuth login for this workflow")
	}
	return nil
}

type sandbox struct {
	Type             string   `json:"type"`
	NetworkAccess    *bool    `json:"networkAccess"`
	WritableRoots    []string `json:"writableRoots,omitempty"`
	ExcludeTmpdirEnv bool     `json:"excludeTmpdirEnvVar,omitempty"`
	ExcludeSlashTmp  bool     `json:"excludeSlashTmp,omitempty"`
}

type threadState struct {
	Model           string  `json:"model"`
	ModelProvider   string  `json:"modelProvider"`
	ReasoningEffort string  `json:"reasoningEffort"`
	ApprovalPolicy  string  `json:"approvalPolicy"`
	Cwd             string  `json:"cwd"`
	Sandbox         sandbox `json:"sandbox"`
	Thread          struct {
		ID             string          `json:"id"`
		SessionID      string          `json:"sessionId"`
		ParentThreadID json.RawMessage `json:"parentThreadId"`
		ForkedFromID   json.RawMessage `json:"forkedFromId"`
		Ephemeral      bool            `json:"ephemeral"`
	} `json:"thread"`
}

func noAncestor(id json.RawMessage) bool { return string(id) == "null" || string(id) == `""` }

func (s *server) startThread(cwd string, role workflow.Role, review bool) (threadState, error) {
	var state threadState
	mode, expected := "workspace-write", "workspaceWrite"
	if review {
		mode, expected = "read-only", "readOnly"
	}
	err := s.request("thread/start", object{
		"cwd": cwd, "model": role.Model, "modelProvider": "openai",
		"config": object{"model_reasoning_effort": role.Effort}, "approvalPolicy": "never",
		"sandbox": mode, "ephemeral": true,
	}, &state, nil)
	if err != nil {
		return state, err
	}
	effectiveCwd, err := filepath.EvalSymlinks(state.Cwd)
	if err != nil || effectiveCwd != cwd || state.Model != role.Model || state.ModelProvider != "openai" ||
		state.ReasoningEffort != role.Effort || state.ApprovalPolicy != "never" || !state.Thread.Ephemeral ||
		!noAncestor(state.Thread.ParentThreadID) || !noAncestor(state.Thread.ForkedFromID) ||
		strings.TrimSpace(state.Thread.ID) == "" || strings.TrimSpace(state.Thread.SessionID) == "" {
		return state, errors.New("Codex returned unexpected model, context, or approval settings")
	}
	policy := state.Sandbox
	if policy.Type != expected || policy.NetworkAccess == nil || *policy.NetworkAccess {
		return state, errors.New("Codex returned an unexpected sandbox policy")
	}
	if !review {
		if policy.WritableRoots == nil {
			return state, errors.New("Codex did not report the implementation sandbox writable roots")
		}
		if !policy.ExcludeTmpdirEnv || !policy.ExcludeSlashTmp {
			return state, errors.New("Codex implementation sandbox permits extra writable roots")
		}
		for _, root := range policy.WritableRoots {
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil || resolved != cwd {
				return state, errors.New("Codex implementation sandbox permits extra writable roots")
			}
		}
	}
	return state, nil
}

type commandResult struct {
	ExitCode *int    `json:"exitCode"`
	Stdout   *string `json:"stdout"`
	Stderr   *string `json:"stderr"`
}

func (s *server) command(cwd string, argv []string, policy sandbox, env object) (commandResult, error) {
	var result commandResult
	deadline, ok := s.ctx.Deadline()
	if !ok {
		return result, errors.New("missing Codex command deadline")
	}
	timeout := time.Until(deadline).Milliseconds()
	if timeout < 1 {
		return result, context.DeadlineExceeded
	}
	err := s.request("command/exec", object{
		"cwd": cwd, "command": argv, "sandboxPolicy": policy, "env": env,
		"timeoutMs": timeout, "outputBytesCap": 100000,
	}, &result, nil)
	if err == nil && (result.ExitCode == nil || result.Stdout == nil || result.Stderr == nil) {
		err = errors.New("Codex command did not return a complete exit result")
	}
	return result, err
}

func (s *server) probeReadOnly(cwd string, policy sandbox) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	target := filepath.Join(cwd, ".nstack-write-probe-"+hex.EncodeToString(id[:]))
	// The pinned POSIX shells report permission errors in C locale. Do not
	// treat a missing parent or exhausted filesystem as a sandbox denial.
	script := `reason=$( (set -C; : > "$1") 2>&1 ) && exit 1
case "$reason" in
  *": Permission denied") printf 'write-denied 13\n';;
  *": Operation not permitted") printf 'write-denied 1\n';;
  *": Read-only file system") printf 'write-denied 30\n';;
  *) exit 2;;
esac`
	result, err := s.command(cwd, []string{"/bin/sh", "-c", script, "nstack-probe", target}, policy, object{"LC_ALL": "C"})
	if _, statErr := os.Lstat(target); statErr == nil {
		removeErr := os.Remove(target)
		return "", errors.Join(errors.New("review sandbox permitted writing in the candidate checkout"), removeErr)
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err != nil {
		return "", err
	}
	proof := strings.TrimSpace(*result.Stdout)
	if *result.ExitCode != 0 || (proof != "write-denied 13" && proof != "write-denied 1" && proof != "write-denied 30") {
		return "", errors.New("could not verify review sandbox write denial")
	}
	return proof, nil
}

func (s *server) gate(cwd string, argv []string, scratch string) (commandResult, error) {
	network := false
	policy := sandbox{Type: "workspaceWrite", NetworkAccess: &network, WritableRoots: []string{cwd, scratch},
		ExcludeTmpdirEnv: true, ExcludeSlashTmp: true}
	return s.command(cwd, argv, policy, object{"TMPDIR": scratch, "PYTHONDONTWRITEBYTECODE": "1"})
}

func (s *server) probeScratch(cwd, parent string) error {
	scratch, err := os.MkdirTemp(parent, "gate-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	target := filepath.Join(scratch, "write-probe")
	result, err := s.gate(cwd, []string{"/bin/sh", "-c", `set -C; : > "$1"`, "nstack-probe", target}, scratch)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if *result.ExitCode != 0 || err != nil || !info.Mode().IsRegular() {
		return errors.New("could not verify quality gate scratch writes")
	}
	return nil
}

func (a *Adapter) Preflight(ctx context.Context, cwd string, reviewer workflow.Role, evidence string) (_ workflow.Evidence, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	s, disabled, err := open(ctx, cwd, evidence, true)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, s.close()) }()
	if err := s.checkRole(reviewer); err != nil {
		return nil, err
	}
	state, err := s.startThread(cwd, reviewer, true)
	if err != nil {
		return nil, err
	}
	proof, err := s.probeReadOnly(cwd, state.Sandbox)
	if err != nil {
		return nil, err
	}
	if err := s.probeScratch(cwd, filepath.Dir(evidence)); err != nil {
		return nil, err
	}
	return workflow.Evidence{"disabledMcpServers": disabled, "writeProbe": proof, "gateScratchWrite": true,
		"roles": []object{{"model": state.Model, "effort": state.ReasoningEffort, "sandbox": state.Sandbox.Type}}}, ctx.Err()
}

func (a *Adapter) Gate(ctx context.Context, req workflow.GateRequest) (_ workflow.GateResult, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	s, _, err := open(ctx, req.Directory, req.Evidence, true)
	if err != nil {
		return workflow.GateResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, s.close()) }()
	result, err := s.gate(req.Directory, req.Argv, req.Scratch)
	if err != nil {
		return workflow.GateResult{}, err
	}
	return workflow.GateResult{ExitCode: result.ExitCode, Evidence: workflow.Evidence{"stdout": *result.Stdout, "stderr": *result.Stderr}}, ctx.Err()
}

func (a *Adapter) Run(ctx context.Context, req workflow.TurnRequest) (_ workflow.TurnResult, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	result := workflow.TurnResult{}
	s, _, err := open(ctx, req.Directory, req.Evidence, true)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, s.close()) }()
	if err := s.checkRole(req.Role); err != nil {
		return result, err
	}
	review := len(req.Schema) != 0
	state, err := s.startThread(req.Directory, req.Role, review)
	if err != nil {
		return result, err
	}
	var proof any
	if review {
		proof, err = s.probeReadOnly(req.Directory, state.Sandbox)
		if err != nil {
			return result, err
		}
	}
	raw := turnStream{thread: state.Thread.ID}
	result = workflow.TurnResult{Model: state.Model, Effort: state.ReasoningEffort, Session: state.Thread.SessionID}
	err = raw.run(s, req)
	result.Message = raw.message
	result.Evidence = workflow.Evidence{"thread": raw.thread, "turn": raw.turn, "sandbox": state.Sandbox,
		"writeProbe": proof, "usage": raw.usage}
	return result, err
}

var _ workflow.Adapter = (*Adapter)(nil)

func unexpectedTurn(detail string) error {
	return fmt.Errorf("invalid Codex turn evidence: %s", detail)
}
