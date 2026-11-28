package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type changesRequested struct{ candidate string }

func (*changesRequested) Error() string {
	return "independent review requested changes in review-only mode"
}

func writeEvidence(path string, value Evidence) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return errors.Join(encoder.Encode(value), file.Close())
}

func optionalText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func validateRole(role Role) error {
	if strings.TrimSpace(role.Model) == "" || strings.TrimSpace(role.Effort) == "" {
		return fmt.Errorf("%s: model and reasoning effort must not be blank", role.Name)
	}
	return nil
}

func within[T any](ctx context.Context, timeout time.Duration, stage string, run func(context.Context) (T, error)) (T, error) {
	ctx, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	var result T
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%s: %w", stage, err)
	}
	result, err := run(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
		err = errors.Join(ctxErr, err)
	}
	if err != nil {
		return result, fmt.Errorf("%s: %w", stage, err)
	}
	return result, nil
}

type workflow struct {
	options
	adapter  Adapter
	repo     gitRepo
	start    string
	original string
	task     string
	work     string
	delivery *delivery
	registry findingRegistry
	sessions map[string]bool
}

func runLocked(ctx context.Context, o options, factory AdapterFactory) (string, error) {
	repo := gitRepo{o.repo}
	lock, err := repo.lock(ctx)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if factory == nil {
		return "", errors.New("required harness adapter is unavailable")
	}
	adapter, err := factory(o.timeout)
	if err != nil {
		return "", err
	}
	if adapter == nil || strings.TrimSpace(adapter.Name()) == "" {
		return "", errors.New("required harness adapter is unavailable")
	}
	w := workflow{options: o, adapter: adapter, repo: repo, sessions: map[string]bool{}}
	if o.mode != reviewOnly {
		w.registry = findingRegistry{}
	}
	return w.execute(ctx)
}

func (w *workflow) prepare(ctx context.Context) error {
	var err error
	if w.start, err = w.repo.head(ctx); err != nil {
		return err
	}
	if err := w.repo.cleanAt(ctx, w.start); err != nil {
		return err
	}
	if w.base, err = w.repo.git(ctx, "rev-parse", "--verify", "--end-of-options", w.base+"^{commit}"); err != nil {
		return err
	}
	if _, err := w.repo.git(ctx, "merge-base", "--is-ancestor", w.base, w.start); err != nil {
		return err
	}
	if w.original, err = w.repo.ref(ctx); err != nil {
		return err
	}
	brief, err := os.ReadFile(w.brief)
	if err != nil {
		return err
	}
	w.task = strings.TrimSpace(string(brief))
	if w.task == "" {
		return errors.New("task brief is empty")
	}
	roles := []Role{w.reviewer}
	if w.mode != reviewOnly {
		roles = []Role{w.writer, w.reviewer}
	}
	var number any
	if w.prNumber != 0 {
		number = w.prNumber
	}
	if err := writeEvidence(filepath.Join(w.evidence, "request.json"), Evidence{
		"adapter": w.adapter.Name(), "start": w.start, "base": w.base, "gate": w.gate, "roles": roles,
		"reviewOnly": w.mode == reviewOnly, "reviewFirst": w.mode == reviewFirst, "maxRemediations": w.remediations,
		"originalRef": optionalText(w.original), "workRef": optionalText(w.workRef),
		"deliveryRef": optionalText(w.deliveryRef), "prNumber": number,
	}); err != nil {
		return err
	}
	if err := validateRole(w.reviewer); err != nil {
		return err
	}
	probe, err := within(ctx, w.timeout, "preflight", func(ctx context.Context) (Evidence, error) {
		return w.adapter.Preflight(ctx, w.repo.directory, w.reviewer, filepath.Join(w.evidence, "preflight"))
	})
	if err != nil {
		return err
	}
	if probe == nil {
		return errors.New("missing preflight evidence")
	}
	return writeEvidence(filepath.Join(w.evidence, "preflight.json"), probe)
}

func (w *workflow) execute(ctx context.Context) (string, error) {
	if err := w.prepare(ctx); err != nil {
		return "", err
	}
	scratch, err := os.MkdirTemp(w.evidence, "checkouts-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	budget := w.remediations
	if w.mode == reviewOnly {
		budget = 0
	}
	last := w.start
	var findings map[string]finding
	for attempt := 0; attempt <= budget; attempt++ {
		if w.mode == implement || findings != nil {
			if err := w.implement(ctx, attempt, last, findings); err != nil {
				return "", err
			}
		}
		candidate, err := w.repo.head(ctx)
		if err != nil {
			return "", err
		}
		if err := w.checkCandidate(ctx, candidate); err != nil {
			return "", err
		}
		if candidate == w.base {
			return "", errors.New("review candidate has no commits beyond the base")
		}
		if err := w.runGate(ctx, scratch, attempt, candidate); err != nil {
			return "", err
		}
		review, newFindings, err := w.review(ctx, scratch, attempt, candidate)
		if err != nil {
			return "", err
		}
		switch review.Status {
		case "pass":
			return candidate, w.deliver(ctx, candidate)
		case "blocked":
			return "", errors.New("independent review blocked this candidate")
		}
		if w.mode == reviewOnly {
			return "", &changesRequested{candidate}
		}
		findings, last = newFindings, candidate
	}
	return "", errors.New("independent review remediation budget exhausted")
}

func (w *workflow) openWriter(ctx context.Context, last string) error {
	ref := w.workRef
	if err := w.repo.branchRef(ctx, ref); err != nil {
		return err
	}
	existing, err := w.repo.git(ctx, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return err
	}
	if existing != "" {
		return errors.New("authorized work ref must be new")
	}
	if err := w.checkCandidate(ctx, last); err != nil {
		return err
	}
	if w.push != "" {
		if w.delivery, err = newDelivery(ctx, w.repo, w.options, w.base, w.start, true); err != nil {
			return err
		}
		if ref == w.delivery.defaultRef || ref == w.delivery.options.deliveryRef || ref == w.delivery.prBaseRef {
			return errors.New("work ref must be distinct from delivery and protected refs")
		}
	}
	if err := w.checkCandidate(ctx, last); err != nil {
		return err
	}
	if _, err := w.repo.git(ctx, "switch", "-c", strings.TrimPrefix(ref, "refs/heads/"), last); err != nil {
		return err
	}
	w.work = ref
	return nil
}

func (w *workflow) checkWriter(ctx context.Context, last string) error {
	if w.delivery != nil {
		if err := w.delivery.check(ctx, last); err != nil {
			return err
		}
	}
	return w.repo.checkWork(ctx, w.work, last, w.original, w.start)
}

func (w *workflow) implement(ctx context.Context, attempt int, last string, findings map[string]finding) error {
	if err := validateRole(w.writer); err != nil {
		return err
	}
	if err := w.repo.cleanAt(ctx, last); err != nil {
		return err
	}
	if w.work == "" {
		if err := w.openWriter(ctx, last); err != nil {
			return err
		}
	}
	if err := w.checkWriter(ctx, last); err != nil {
		return err
	}
	prompt := fmt.Sprintf("Implement this task in the current feature checkout. Base: %s.\n"+
		"Only edit task files. The caller owns Git commits, quality gates, and remote delivery. "+
		"Use no subagents or external services.\n\nTask and acceptance criteria:\n%s", w.base, w.task)
	message := "Implement requested change"
	if findings != nil {
		encoded, err := json.Marshal(findings)
		if err != nil {
			return err
		}
		prompt += fmt.Sprintf("\n\nFix demonstrated findings for %s:\n%s", last, encoded)
		message = "Address independent review findings"
	}
	path := w.stagePath(attempt, "implementation")
	result, err := w.runTurn(ctx, TurnRequest{Directory: w.repo.directory, Role: w.writer, Prompt: prompt, Evidence: path})
	if err != nil {
		return err
	}
	record := turnEvidence(result)
	record["start"], record["base"], record["workRef"] = last, w.base, w.work
	if err := writeEvidence(path+".json", record); err != nil {
		return err
	}
	if err := w.checkWriter(ctx, last); err != nil {
		return err
	}
	if _, err := w.repo.git(ctx, "add", "-A"); err != nil {
		return err
	}
	changed, err := w.repo.git(ctx, "diff", "--cached", "--name-only")
	if err != nil {
		return err
	}
	if changed == "" {
		return errors.New("implementation made no committable change")
	}
	if err := w.checkWriter(ctx, last); err != nil {
		return err
	}
	if _, err := w.repo.git(ctx, "commit", "-m", message); err != nil {
		return err
	}
	parent, err := w.repo.git(ctx, "rev-parse", "HEAD^")
	if err != nil {
		return err
	}
	if parent != last {
		return errors.New("implementation commit did not extend the authorized candidate")
	}
	return nil
}

func (w *workflow) checkCandidate(ctx context.Context, candidate string) error {
	if !objectID.MatchString(candidate) {
		return errors.New("invalid candidate SHA")
	}
	if err := w.repo.cleanAt(ctx, candidate); err != nil {
		return err
	}
	if w.work != "" {
		return w.repo.checkWork(ctx, w.work, candidate, w.original, w.start)
	}
	ref, err := w.repo.ref(ctx)
	if err != nil {
		return err
	}
	if ref != w.original || candidate != w.start {
		return errors.New("starting ref or candidate changed")
	}
	return nil
}

func (w *workflow) stagePath(attempt int, phase string) string {
	return filepath.Join(w.evidence, fmt.Sprintf("%d-%s", attempt, phase))
}

func (w *workflow) runGate(ctx context.Context, scratch string, attempt int, candidate string) error {
	checkout, err := w.repo.cloneAt(ctx, filepath.Join(scratch, fmt.Sprintf("%d-gate", attempt)), candidate)
	if err != nil {
		return err
	}
	gateDir := filepath.Join(scratch, fmt.Sprintf("%d-scratch", attempt))
	if err := os.Mkdir(gateDir, 0o700); err != nil {
		return err
	}
	path := w.stagePath(attempt, "gate")
	result, err := within(ctx, w.timeout, "gate", func(ctx context.Context) (GateResult, error) {
		return w.adapter.Gate(ctx, GateRequest{checkout.directory, w.gate, gateDir, path})
	})
	if err != nil {
		return err
	}
	record := Evidence{}
	maps.Copy(record, result.Evidence)
	record["candidate"], record["base"] = candidate, w.base
	if result.ExitCode == nil {
		delete(record, "exitCode")
		err := errors.New("gate result is missing an exit code")
		record["validationError"] = err.Error()
		return errors.Join(err, writeEvidence(path+".json", record))
	}
	record["exitCode"] = *result.ExitCode
	if err := writeEvidence(path+".json", record); err != nil {
		return err
	}
	if err := checkout.cleanAt(ctx, candidate); err != nil {
		return err
	}
	if *result.ExitCode != 0 {
		return errors.New("candidate quality gate failed")
	}
	return w.checkCandidate(ctx, candidate)
}

func (w *workflow) runTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	result, err := within(ctx, w.timeout, "turn", func(ctx context.Context) (TurnResult, error) {
		return w.adapter.Run(ctx, request)
	})
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(result.Session) == "" || strings.TrimSpace(result.Message) == "" ||
		result.Model != request.Role.Model || result.Effort != request.Role.Effort {
		return result, errors.New("turn evidence does not match the requested role or session")
	}
	if w.sessions[result.Session] {
		return result, errors.New("review reused an existing context")
	}
	w.sessions[result.Session] = true
	return result, nil
}

func turnEvidence(result TurnResult) Evidence {
	record := Evidence{}
	maps.Copy(record, result.Evidence)
	record["session"], record["message"] = result.Session, result.Message
	record["model"], record["effort"] = result.Model, result.Effort
	return record
}

func (w *workflow) review(ctx context.Context, scratch string, attempt int, candidate string) (verdict, map[string]finding, error) {
	var review verdict
	checkout, err := w.repo.cloneAt(ctx, filepath.Join(scratch, fmt.Sprintf("%d-review", attempt)), candidate)
	if err != nil {
		return review, nil, err
	}
	prompt := fmt.Sprintf("Independently review exactly candidate %s against base %s. Verify HEAD. "+
		"Inspect the diff and actual behavior where possible in this read-only checkout. "+
		"Use no subagents or external services. The caller already ran the quality gate. "+
		"Return the full candidate SHA and a structured verdict. Use changes_requested only "+
		"for demonstrated acceptance defects; include location, evidence (actual behavior and "+
		"reproduction if feasible), expected behavior and impact for each. Use blocked for "+
		"insufficient evidence or required contract decisions; use pass when the contract holds.\n\n"+
		"Task and acceptance criteria:\n%s", candidate, w.base, w.task)
	if w.registry != nil {
		registry, err := json.Marshal(w.registry)
		if err != nil {
			return review, nil, err
		}
		prompt += "\n\nIndependently discover new blocking defects. Put only new defects in findings. " +
			"Assess EVERY registered ID once in previous with resolved, recurring, or uncertain " +
			"and candidate-specific evidence. Keep the ID for the same defect even if the location " +
			"or reproduction changes. If equivalence or resolution cannot be determined use uncertain. " +
			"A pass requires all previous findings resolved and no new findings. Registry: " + string(registry)
	}
	path := w.stagePath(attempt, "review")
	result, err := w.runTurn(ctx, TurnRequest{checkout.directory, w.reviewer, prompt, path, reviewSchema(w.registry != nil)})
	record := turnEvidence(result)
	record["candidate"], record["base"] = candidate, w.base
	if err == nil {
		err = checkout.cleanAt(ctx, candidate)
	}
	if err == nil {
		err = w.checkCandidate(ctx, candidate)
	}
	if err == nil {
		review, err = validateVerdict(result.Message, candidate, w.registry)
	}
	if err != nil {
		record["validationError"] = err.Error()
		return review, nil, errors.Join(err, writeEvidence(path+".json", record))
	}
	newFindings := map[string]finding{}
	if w.registry != nil {
		for _, item := range review.Findings {
			id := fmt.Sprintf("F%d", len(w.registry)+1)
			w.registry[id] = recordedFinding{item["location"], item["expected"], item["impact"], item["evidence"]}
			newFindings[id] = item
		}
	}
	record["review"], record["newFindingIds"] = review.Raw, newFindings
	if err := writeEvidence(path+".json", record); err != nil {
		return review, nil, err
	}
	for _, item := range review.Previous {
		if item["status"] != "resolved" {
			return review, nil, errors.New("previous finding recurs or remains uncertain; no further remediation")
		}
	}
	return review, newFindings, nil
}

func (w *workflow) deliver(ctx context.Context, candidate string) error {
	if err := w.checkCandidate(ctx, candidate); err != nil {
		return err
	}
	if w.push == "" {
		return nil
	}
	if w.delivery == nil {
		var err error
		w.delivery, err = newDelivery(ctx, w.repo, w.options, w.base, w.start, false)
		if err != nil {
			return err
		}
	}
	return w.delivery.push(ctx, candidate)
}
