package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleTOMLContract(t *testing.T) {
	// Expectations are from the v0.4.0 Python/Go parity table. Each model
	// advertises exactly one effort, so a pass also proves effort resolution.
	t.Setenv("PATH", "")
	dir := t.TempDir()
	role := filepath.Join(dir, "role.toml")
	const catalog = `{"data":[
		{"model":"configured","supportedReasoningEfforts":[{"reasoningEffort":"high"}]},
		{"model":"inherited","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]},
		{"model":"optional","hidden":true,"modelSpecialty":"cyber","availableAccessPrograms":{"cyber":["daybreakBlue"]},
		 "supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}]}`
	const name = "name = \"review\"\n"
	const canonical = "model = \"configured\"\nmodel_reasoning_effort = \"high\"\n"
	const optional = "name = \"nstack_security_daybreak\"\nmodel = \"optional\"\n"
	for _, c := range []struct {
		label, source, inherited, advertised, diagnostic string
		optIn                                            bool
	}{
		{"canonical", name + canonical, "inherited", "review: configured", "", false},
		{"uppercase model", name + "MODEL = \"configured\"\nmodel_reasoning_effort = \"high\"\n", "absent", "", "absent is absent", false},
		{"uppercase effort", name + "MODEL_REASONING_EFFORT = \"high\"\n", "inherited", "review: inherited", "", false},
		{"uppercase name", "NAME = \"review\"\n", "inherited", "", "", false},
		{"mixed case", name + "\"MoDeL\" = \"configured\"\nModel_Reasoning_Effort = \"high\"\n", "inherited", "review: inherited", "", false},
		{"canonical and aliases", name + canonical + "NAME = \"other\"\nMODEL = \"inherited\"\nMODEL_REASONING_EFFORT = \"medium\"\n", "inherited", "review: configured", "", false},
		{"unknown keys", name + "unrecognized = 42\nMODE = false\n", "inherited", "review: inherited", "", false},
		{"inherited", name, "inherited", "review: inherited", "", false},
		{"inherited model explicit effort", name + "model_reasoning_effort = \"high\"\n", "inherited", "", "does not advertise reasoning effort high", false},
		{"explicit model inherited effort", name + "model = \"configured\"\n", "inherited", "", "does not advertise reasoning effort medium", false},
		{"invalid model type", name + "model = 42\n", "inherited", "", "", false},
		{"invalid effort type", name + "model_reasoning_effort = 42\n", "inherited", "", "", false},
		{"empty explicit model", name + "model = \"\"\n", "inherited", "", "resolve the inherited model", false},
		{"unknown model", name + "model = \"missing\"\n", "inherited", "", "missing is absent", false},
		{"duplicate canonical", name + canonical + "\"model\" = \"inherited\"\n", "inherited", "", "", false},
		{"duplicate ignored", name + "MODEL = \"inherited\"\n\"MODEL\" = \"configured\"\n", "inherited", "", "", false},
		{"nested table", name + "[role]\nname = \"other\"\n" + canonical, "inherited", "review: inherited", "", false},
		{"nested alias", name + "MODEL.value = \"configured\"\n", "inherited", "review: inherited", "", false},
		{"invalid nested canonical", name + "model.value = \"configured\"\n", "inherited", "", "", false},
		{"literal dotted key", name + "\"model.value\" = \"configured\"\n", "inherited", "review: inherited", "", false},
		{"alias cannot grant access", name + "NAME = \"nstack_security_daybreak\"\nmodel = \"optional\"\n", "inherited", "", "requires the optional defensive role", true},
		{"alias cannot remove opt-in", optional + "NAME = \"review\"\n", "inherited", "", "explicitly select", false},
		{"canonical opt-in", optional, "inherited", "nstack_security_daybreak: optional", "", true},
	} {
		t.Run(c.label, func(t *testing.T) {
			fixtureWrite(role, c.source)
			args := []string{role, "--inherited-model", c.inherited, "--inherited-effort", "medium"}
			if c.optIn {
				args = append(args, "--daybreak-selected")
			}
			assertRole(t, args, catalog, c.advertised, c.diagnostic, 1)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || fixtureRead(role) != c.source {
				t.Fatalf("role check changed fixture directory: %v %v", entries, err)
			}
		})
	}
}

func assertRole(t *testing.T, args []string, catalog, advertised, diagnostic string, failureCode int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := CheckRoleCLI(args, strings.NewReader(catalog), &stdout, &stderr)
	if advertised != "" {
		if code != 0 || stderr.Len() != 0 || stdout.String() != advertised+" advertised (provider response unverified)\n" {
			t.Fatalf("unexpected role result: exit=%d stdout=%q stderr=%q", code, &stdout, &stderr)
		}
	} else if code != failureCode || stdout.Len() != 0 || !strings.Contains(stderr.String(), diagnostic) ||
		failureCode == 1 && !strings.HasPrefix(stderr.String(), "role unavailable: ") {
		t.Fatalf("want exit=%d diagnostic=%q; got exit=%d stdout=%q stderr=%q", failureCode, diagnostic, code, &stdout, &stderr)
	}
}

func TestRoleCatalogContract(t *testing.T) {
	t.Setenv("PATH", "")
	const normal = `{"model":"gpt-6.2-sol","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}`
	const special = `{"model":"gpt-daybreak-blue-latest","modelSpecialty":"cyber","hidden":true,"supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}`
	withAccess := strings.TrimSuffix(special, "}") + `,"availableAccessPrograms":{"cyber":["daybreakBlue"]}}`
	const standard = "../../examples/agents/nstack_security.toml"
	const optional = "../../adapters/codex/optional/nstack_security_daybreak.toml"
	personal := filepath.Join(t.TempDir(), "role.toml")
	fixtureWrite(personal, "name = \"nstack_implement\"\n")
	for _, c := range []struct {
		name                            string
		args                            []string
		catalog, advertised, diagnostic string
		failure                         int
	}{
		{"standard", []string{standard}, `{"data":[` + normal + `,` + special + `]}`, "nstack_security: gpt-6.2-sol", "", 1},
		{"absent", []string{standard}, `{"data":[` + special + `]}`, "", "absent", 1},
		{"optional no advertised grant", []string{optional, "--daybreak-selected"}, `{"data":[` + special + `]}`, "", "does not advertise", 1},
		{"optional no explicit choice", []string{optional}, `{"data":[` + withAccess + `]}`, "", "explicitly select", 1},
		{"optional advertised", []string{optional, "--daybreak-selected"}, `{"data":[` + withAccess + `]}`, "nstack_security_daybreak: gpt-daybreak-blue-latest", "", 1},
		{"wrapped catalog", []string{standard}, `{"result":{"data":[` + normal + `]}}`, "nstack_security: gpt-6.2-sol", "", 1},
		{"partial catalog", []string{standard}, `{"data":[` + normal + `],"nextCursor":"more"}`, "", "collect all model/list pages", 1},
		{"inherited model/effort", []string{personal, "--inherited-model", "gpt-6.2-sol", "--inherited-effort", "medium"}, `{"data":[` + normal + `]}`, "nstack_implement: gpt-6.2-sol", "", 1},
		{"flags before and after", []string{"--inherited-model", "gpt-6.2-sol", personal, "--inherited-effort", "medium"}, `{"data":[` + normal + `]}`, "nstack_implement: gpt-6.2-sol", "", 1},
		{"unsupported effort", []string{personal, "--inherited-model", "gpt-6.2-sol", "--inherited-effort", "high"}, `{"data":[` + normal + `]}`, "", "reasoning effort high", 1},
		{"missing path", nil, `{"data":[]}`, "", "exactly one role TOML", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertRole(t, c.args, c.catalog, c.advertised, c.diagnostic, c.failure)
		})
	}
}
