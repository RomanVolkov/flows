package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/romanvolkov/flows/internal/paths"
)

const SpecVersion = "1"

// SpecPath returns the effective installed spec directory for version.
func SpecPath(home paths.Home, version string) string {
	if version == "" {
		version = SpecVersion
	}
	return filepath.Join(home.Spec(), "v"+strings.TrimPrefix(version, "v"))
}

// SpecShow reads a spec artifact ("authoring", "protocol", "workflow", "tool")
// from the installed spec/v1 directory.
func SpecShow(home paths.Home, topic string) ([]byte, error) {
	base := SpecPath(home, SpecVersion)
	var file string
	switch topic {
	case "authoring", "":
		file = filepath.Join(base, "authoring.md")
	case "protocol":
		file = filepath.Join(base, "protocol.md")
	case "workflow":
		file = filepath.Join(base, "workflow.schema.json")
	case "tool":
		file = filepath.Join(base, "tool.schema.json")
	default:
		return nil, fmt.Errorf("unknown spec topic %q (expected authoring, protocol, workflow, or tool)", topic)
	}
	return os.ReadFile(file)
}

// InstallSpec atomically stages the v1 authoring guide, protocol docs, JSON schemas,
// examples, and Go SDK into home.Spec()/v1 for offline use.
func InstallSpec(home paths.Home, repoRoot string) error {
	if err := home.Init(); err != nil {
		return err
	}
	target := SpecPath(home, SpecVersion)
	stage := target + ".staging"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}

	if err := copyDir(filepath.Join(repoRoot, "spec", "v1"), stage); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if err := copyFile(filepath.Join(repoRoot, "schema", "workflow-v1.json"), filepath.Join(stage, "workflow.schema.json")); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if err := copyFile(filepath.Join(repoRoot, "schema", "tool-v1.json"), filepath.Join(stage, "tool.schema.json")); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if err := copyDir(filepath.Join(repoRoot, "sdk", "go"), filepath.Join(stage, "sdk", "go")); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}

	_ = os.RemoveAll(target)
	return os.Rename(stage, target)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0700)
		}
		return copyFile(p, out)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
