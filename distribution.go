// Package distribution provides the canonical nstack assets embedded in the binary.
package distribution

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
)

// Use the published asset locations rather than embedding the checkout.
//
//go:embed skills/*/SKILL.md skills/*/agents/openai.yaml skills/natori-mode/playbooks/*.md
//go:embed adapters/codex/optional/*.toml
//go:embed docs/*.md docs/releases/*.md examples/agents/*.toml
//go:embed LICENSE LICENSE-upstream README.md README.ja.md .codex-plugin/plugin.json
var content embed.FS

const manifest = ".codex-plugin/plugin.json"

// Snapshot is the published installer payload and embedded asset identity.
type Snapshot struct {
	Files    map[string][]byte
	Version  string
	Revision string
}

func Bundled() (Snapshot, error) {
	result := Snapshot{Files: make(map[string][]byte)}
	var plugin struct {
		Version string `json:"version"`
	}
	hash := sha256.New()
	err := fs.WalkDir(content, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return err
		}
		// WalkDir sorts paths; length delimiters make the revision unambiguous.
		fmt.Fprintf(hash, "%d:%s%d:", len(path), path, len(data))
		hash.Write(data)
		if path == manifest {
			return json.Unmarshal(data, &plugin)
		}
		result.Files[path] = data
		return nil
	})
	result.Version = plugin.Version
	result.Revision = hex.EncodeToString(hash.Sum(nil))
	if err == nil && result.Version == "" {
		err = fmt.Errorf("embedded distribution manifest has no version")
	}
	return result, err
}
