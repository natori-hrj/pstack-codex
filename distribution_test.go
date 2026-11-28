package distribution

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

func TestBundledAssets(t *testing.T) {
	paths, err := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
		"skills", "adapters/codex/optional", "docs", "examples/agents",
		"LICENSE", "LICENSE-upstream", "README.md", "README.ja.md", manifest).Output()
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]bool)
	for _, path := range strings.Split(strings.TrimSuffix(string(paths), "\x00"), "\x00") {
		expected[path] = true
	}
	if err := fs.WalkDir(content, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !expected[path] {
			t.Errorf("untracked or unexpected embedded asset %s", path)
		}
		delete(expected, path)
		embedded, err := content.ReadFile(path)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(path)
		if err == nil && !bytes.Equal(embedded, actual) {
			t.Errorf("embedded contents differ: %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("missing embedded assets: %v", expected)
	}
}

func TestDistributionCatalog(t *testing.T) {
	skills, err := fs.Glob(content, "skills/*/SKILL.md")
	if err != nil || len(skills) != 26 {
		t.Fatalf("expected 26 skills, found %d: %v", len(skills), err)
	}
	relativeLink := regexp.MustCompile(`\]\((\.\.?/[^)#]+)`)
	names := map[string]bool{}
	for _, path := range skills {
		text := string(readFile(t, path))
		parts := strings.SplitN(text, "---\n", 3)
		if len(parts) != 3 || parts[0] != "" || strings.TrimSpace(parts[2]) == "" {
			t.Fatalf("missing metadata or instructions in %s", path)
		}
		var meta struct{ Name, Description string }
		if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if meta.Name != filepath.Base(filepath.Dir(path)) || strings.TrimSpace(meta.Description) == "" || names[meta.Name] {
			t.Fatalf("invalid skill metadata in %s: %+v", path, meta)
		}
		names[meta.Name] = true
		for _, link := range relativeLink.FindAllStringSubmatch(text, -1) {
			resolved := filepath.Join(filepath.Dir(path), link[1])
			if stat, err := fs.Stat(content, resolved); err != nil || !stat.Mode().IsRegular() {
				t.Errorf("broken relative link in %s: %s", path, link[1])
			}
		}
	}
	if !names["natori-mode"] || !names["setup-nstack"] || names["poteto-mode"] || names["setup-pstack"] {
		t.Fatal("incorrect nstack skill names")
	}
	for _, path := range []string{"LICENSE", "LICENSE-upstream"} {
		if !bytes.Contains(readFile(t, path), []byte("Permission is hereby granted")) {
			t.Errorf("missing MIT notice in %s", path)
		}
	}
	swarm := string(readFile(t, "skills/swarm/SKILL.md"))
	if strings.Contains(swarm, "Codex") || !strings.Contains(swarm, "active harness adapter") ||
		!strings.Contains(swarm, "Git worktree") || !strings.Contains(swarm, "without granting sibling write access") {
		t.Fatal("swarm lost its portable ownership and isolation instructions")
	}
}

func TestCodexDistributionMetadata(t *testing.T) {
	var plugin struct{ Name, Version, Skills string }
	if err := json.Unmarshal(readFile(t, manifest), &plugin); err != nil {
		t.Fatal(err)
	}
	if plugin.Name != "nstack" || plugin.Version != "0.4.1" || filepath.Clean(plugin.Skills) != "skills" {
		t.Fatalf("invalid plugin manifest: %+v", plugin)
	}
	roles, err := fs.Glob(content, "examples/agents/*.toml")
	if err != nil || len(roles) != 3 {
		t.Fatalf("incorrect example roles: %v, %v", roles, err)
	}
	for _, name := range []string{"nstack_implement", "nstack_review", "nstack_security"} {
		var config map[string]any
		if _, err := toml.Decode(string(readFile(t, "examples/agents/"+name+".toml")), &config); err != nil {
			t.Fatal(err)
		}
		model, _ := config["model"].(string)
		if config["name"] != name || model == "" {
			t.Errorf("invalid role example: %v", config)
		}
		if name == "nstack_review" {
			instructions, _ := config["developer_instructions"].(string)
			if _, hasSandbox := config["sandbox_mode"]; hasSandbox || !strings.Contains(instructions, "read-only parent") {
				t.Errorf("reviewer does not rely on enforced read-only parent: %v", config)
			}
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := content.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReadmeCodexInstallation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "nstack")
	const stub = `#!/bin/sh
if [ "$1" = --version ]; then printf '%s\n' 'nstack 0.4.1 assets sha256:fixture'; exit 0; fi
printf '%s\n' "$4" >> "$INSTALL_TRACE"
[ "$4" != "$FAIL_INSTALL_STEP" ] || exit 17
`
	if err := os.WriteFile(binary, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	for _, readme := range []string{"README.md", "README.ja.md"} {
		_, after, ok := strings.Cut(string(readFile(t, readme)), "```sh\n")
		block, _, closed := strings.Cut(after, "```")
		if !ok || !closed {
			t.Fatalf("installation block missing in %s", readme)
		}
		const fullTrace = "stage\nactivate\n"
		for _, c := range []struct {
			name, failure, trace string
			optIn, existing      bool
		}{
			{"stage failure", "stage", "stage\n", true, false},
			{"activation failure", "activate", fullTrace, true, false},
			{"default skills only", "", fullTrace, false, false},
			{"fresh home with roles", "", fullTrace, true, false},
			{"custom role and dangling link", "", fullTrace, true, true},
		} {
			t.Run(readme+"/"+c.name, func(t *testing.T) {
				private := t.TempDir()
				home, trace := filepath.Join(private, "codex-home"), filepath.Join(private, "trace")
				agents := filepath.Join(home, "agents")
				source := filepath.Join(home, "nstack", "snapshots", "fixture", "examples", "agents")
				if err := os.MkdirAll(source, 0700); err != nil {
					t.Fatal(err)
				}
				names := []string{"nstack_implement.toml", "nstack_review.toml", "nstack_security.toml"}
				for _, name := range names {
					if err := os.WriteFile(filepath.Join(source, name), readFile(t, "examples/agents/"+name), 0600); err != nil {
						t.Fatal(err)
					}
				}
				const customized = "name = \"custom implementation role\"\n"
				missing := filepath.Join(private, "must-not-be-created")
				if c.existing {
					if err := os.Mkdir(agents, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(agents, names[0]), []byte(customized), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(missing, filepath.Join(agents, names[1])); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.Command("/bin/sh", "-c", block)
				cmd.Env = []string{"PATH=" + dir + ":" + os.Getenv("PATH"), "HOME=" + private, "CODEX_HOME=" + home,
					"INSTALL_TRACE=" + trace, "FAIL_INSTALL_STEP=" + c.failure}
				if c.optIn {
					cmd.Env = append(cmd.Env, "NSTACK_INSTALL_EXAMPLE_ROLES=1")
				}
				out, err := cmd.CombinedOutput()
				if c.failure != "" {
					if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
						t.Fatalf("failure not propagated: %v %s", err, out)
					}
				} else if err != nil {
					t.Fatalf("install failed: %v %s", err, out)
				}
				if data, err := os.ReadFile(trace); err != nil || string(data) != c.trace {
					t.Fatalf("unexpected install steps: %q %v", data, err)
				}
				if c.failure != "" || !c.optIn {
					if _, err := os.Lstat(agents); !os.IsNotExist(err) {
						t.Fatalf("wrapper unexpectedly changed installed roles: %v", err)
					}
					return
				}
				for i, name := range names {
					dest := filepath.Join(agents, name)
					if c.existing && i == 1 {
						if target, err := os.Readlink(dest); err != nil || target != missing {
							t.Fatalf("wrapper changed the dangling role link: %q %v", target, err)
						}
						if _, err := os.Stat(missing); !os.IsNotExist(err) {
							t.Fatalf("wrapper wrote to the dangling role target: %v", err)
						}
						continue
					}
					want := readFile(t, "examples/agents/"+name)
					if c.existing && i == 0 {
						want = []byte(customized)
					}
					if data, err := os.ReadFile(dest); err != nil || !bytes.Equal(data, want) {
						t.Fatalf("unexpected role %s: %q %v", name, data, err)
					}
				}
			})
		}
	}
}
