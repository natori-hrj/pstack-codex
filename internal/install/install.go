// Package install stages and activates managed workflow snapshots on Linux.
package install

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/natori-hrj/nstack"
)

var snapshotID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const activeLink = "nstack-managed"

func RunCLI(args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "Explicit harness configuration directory")
	harness := flags.String("harness", "codex", "Skill discovery layout (codex or opencode)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: install [--harness codex|opencode] --home PATH {stage|activate} SNAPSHOT")
	}
	var position []string
	for len(args) != 0 && len(position) <= 2 {
		err := flags.Parse(args)
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if err != nil {
			return 2
		}
		args = flags.Args()
		if len(args) != 0 {
			position = append(position, args[0])
			args = args[1:]
		}
	}
	if *home == "" || len(position) != 2 || (position[0] != "stage" && position[0] != "activate") {
		flags.Usage()
		return 2
	}
	action, id := position[0], position[1]
	if !snapshotID.MatchString(id) {
		fmt.Fprintln(stderr, "invalid snapshot identifier")
		return 2
	}
	if *harness != "codex" && *harness != "opencode" {
		fmt.Fprintln(stderr, "unsupported install harness: expected codex or opencode")
		return 2
	}
	assets, err := distribution.Bundled()
	if err == nil {
		var path string
		path, err = run(*home, action, id, assets.Files, nil, *harness == "opencode")
		if err == nil {
			fmt.Fprintln(out, path)
			return 0
		}
	}
	fmt.Fprintln(stderr, err)
	return 1
}

// checkpoint is an internal observation point for interruption tests. The
// CLI never installs a callback or reads fault-injection environment values.
type checkpoint func(string)

func (c checkpoint) at(name string) {
	if c != nil {
		c(name)
	}
}

func run(homePath, action, id string, assets map[string][]byte, observe checkpoint, flatSkills bool) (string, error) {
	if !snapshotID.MatchString(id) {
		return "", errors.New("invalid snapshot identifier")
	}
	homePath, err := resolveHome(homePath)
	if err != nil {
		return "", err
	}
	home, err := createHome(homePath)
	if err != nil {
		return "", err
	}
	defer home.Close()
	skills, err := home.open("skills", false)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if skills.File != nil {
		defer skills.Close()
	}
	state, err := home.open("nstack", true)
	if err != nil {
		return "", err
	}
	defer state.Close()
	guard, err := lock(state)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	snapshots, err := state.open("snapshots", true)
	if err != nil {
		return "", err
	}
	defer snapshots.Close()

	checkAnchors := func() error {
		if !matches(home.File, homePath) || !matches(state.File, home.path("nstack")) ||
			!matches(guard, state.path("lock")) || !matches(snapshots.File, state.path("snapshots")) ||
			(skills.File != nil && !matches(skills.File, home.path("skills"))) {
			return errors.New("installer paths changed during the operation")
		}
		return nil
	}
	path := filepath.Join(homePath, "nstack", "snapshots", id)
	if action == "stage" {
		return path, stage(state, snapshots, id, assets, checkAnchors, observe)
	}
	snapshot, err := snapshots.open(id, false)
	if err != nil {
		return "", fmt.Errorf("stage snapshot %s first: %w", id, err)
	}
	defer snapshot.Close()
	if _, err := inspect(snapshot, assets, false); err != nil {
		return "", err
	}
	snapshotSkills, err := snapshot.open("skills", false)
	if err != nil {
		return "", err
	}
	defer snapshotSkills.Close()
	names, err := snapshotNames(snapshotSkills)
	if err != nil {
		return "", err
	}
	if skills.File == nil {
		skills, err = home.open("skills", true)
		if err != nil {
			return "", err
		}
		defer skills.Close()
	}
	return path, activate(state, skills, filepath.Dir(path), id, names, func() error {
		if !matches(snapshot.File, snapshots.path(id)) || !matches(snapshotSkills.File, snapshot.path("skills")) {
			return errors.New("snapshot changed during activation")
		}
		return checkAnchors()
	}, observe, flatSkills)
}

func stage(state, snapshots directory, id string, assets map[string][]byte, check func() error, observe checkpoint) error {
	existing, err := snapshots.open(id, false)
	if err == nil {
		defer existing.Close()
		if _, err := inspect(existing, assets, true); err != nil {
			return fmt.Errorf("snapshot %s already contains different files: %w", id, err)
		}
		if !matches(existing.File, snapshots.path(id)) {
			return errors.New("snapshot changed while comparing staged files")
		}
		return check()
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	name, scratch, cleanup, err := temp(state, ".stage-")
	if err != nil {
		return err
	}
	defer cleanup()
	root, err := os.OpenRoot(scratch.path("."))
	if err != nil {
		return err
	}
	defer root.Close()
	started := false
	for path, data := range assets {
		if err := root.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
		if !started {
			observe.at("staging")
			started = true
		}
	}
	// Publish a normal directory from private staging. Apply permissions
	// through the pinned descriptor.
	info, err := state.Stat()
	if err != nil {
		return err
	}
	if err := scratch.Chmod(info.Mode().Perm() & 0755); err != nil {
		return err
	}
	digest, err := inspect(scratch, assets, true)
	if err != nil {
		return err
	}
	// Keep the checksum on the directory inode: it is published atomically
	// with the payload and adds no paths to the snapshot file list.
	if err := syscall.Setxattr(scratch.path("."), integrityAttribute, digest, 0); err != nil {
		return fmt.Errorf("seal staged snapshot: %w", err)
	}
	if err := check(); err != nil {
		return err
	}
	if !matches(scratch.File, state.path(name)) {
		return errors.New("staging directory changed before publication")
	}
	return renameNew(state, name, snapshots, id)
}

const integrityAttribute = "user.nstack.snapshot.sha256"

// The v0.3.0 installer shipped these files and no integrity attribute. Retain
// its completeness checks for unsealed rollback targets, including when a
// current snapshot loses its seal. Sealed targets verify their original bytes.
var legacyUnsealedFiles = [...]string{
	"adapters/codex/check_role.py",
	"adapters/codex/install.py",
	"adapters/codex/review.py",
	"adapters/codex/review_adapter.py",
	"adapters/review_workflow.py",
}

func inspect(snapshot directory, assets map[string][]byte, exact bool) ([]byte, error) {
	var sealed [sha256.Size]byte
	n, err := syscall.Getxattr(snapshot.path("."), integrityAttribute, sealed[:])
	hasSeal := err == nil
	switch {
	case hasSeal && n != len(sealed):
		return nil, errors.New("invalid snapshot integrity attribute")
	case err != nil && err != syscall.ENODATA && err != syscall.ENOTSUP:
		return nil, fmt.Errorf("read snapshot integrity attribute: %w", err)
	}
	root, err := os.OpenRoot(snapshot.path("."))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	sum := sha256.New()
	found := make(map[string]int64)
	directories := map[string]bool{".": true}
	for path := range assets {
		for ancestor := filepath.Dir(path); !directories[ancestor]; ancestor = filepath.Dir(ancestor) {
			directories[ancestor] = true
		}
	}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		if int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe snapshot entry permissions: %s", path)
		}
		if info.IsDir() {
			if exact && !directories[path] {
				return fmt.Errorf("unexpected snapshot directory: %s", path)
			}
			fmt.Fprintf(sum, "D%d:%s", len(path), path)
			return nil
		}
		if !info.Mode().IsRegular() || stat.Nlink != 1 {
			return fmt.Errorf("unsafe snapshot entry: %s", path)
		}
		expected, required := assets[path]
		if exact {
			if !required || info.Size() != int64(len(expected)) {
				return fmt.Errorf("different snapshot entry: %s", path)
			}
		} else if required && info.Size() == 0 {
			return fmt.Errorf("incomplete snapshot entry: %s", path)
		}
		if exact || hasSeal {
			file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer file.Close()
			opened, err := file.Stat()
			if err != nil || !os.SameFile(opened, info) || opened.Size() != info.Size() {
				return fmt.Errorf("snapshot entry changed while opening: %s", path)
			}
			fmt.Fprintf(sum, "F%d:%s%d:", len(path), path, info.Size())
			if exact {
				actual, err := io.ReadAll(io.LimitReader(file, info.Size()+1))
				if err != nil || !bytes.Equal(actual, expected) {
					return fmt.Errorf("different snapshot entry: %s", path)
				}
				sum.Write(actual)
			} else if written, err := io.Copy(sum, io.LimitReader(file, info.Size()+1)); err != nil || written != info.Size() {
				return fmt.Errorf("different snapshot entry: %s", path)
			}
		}
		found[path] = info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	for path := range assets {
		if !exact && rollbackOptionalDoc(path, hasSeal) {
			continue
		}
		if _, exists := found[path]; !exists {
			return nil, fmt.Errorf("incomplete snapshot: missing %s", path)
		}
	}
	if !exact && !hasSeal {
		for _, path := range legacyUnsealedFiles {
			if found[path] == 0 {
				return nil, fmt.Errorf("incomplete unsealed snapshot: missing or empty %s", path)
			}
		}
	}
	digest := sum.Sum(nil)
	if hasSeal && !bytes.Equal(digest, sealed[:]) {
		return nil, errors.New("snapshot integrity checksum does not match")
	}
	return digest, nil
}

// These documents were introduced after snapshots that remain rollback targets.
// Exact staging still requires them, and sealed snapshots still verify every byte.
func rollbackOptionalDoc(path string, sealed bool) bool {
	switch path {
	case "docs/opencode.md", "docs/opencode.ja.md", "docs/releases/v0.4.0.md", "docs/releases/v0.4.0.ja.md",
		"docs/releases/v0.4.1.md", "docs/releases/v0.4.1.ja.md":
		return true
	case "docs/behavior-v0.3.0.md":
		return !sealed
	default:
		return false
	}
}
