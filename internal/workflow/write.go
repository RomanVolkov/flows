package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/romanvolkov/flows/internal/catalog"
)

var ErrExternalEdit = errors.New("workflow file modified externally since load")

// SaveResult describes a validated atomic YAML save.
type SaveResult struct {
	Digest  string `json:"digest"`
	SavedAt int64  `json:"saved_at"`
}

// DigestBytes returns the hex SHA-256 digest of data.
func DigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// EncodeSyntaxTree serializes a yaml.Node tree preserving comments and key order.
func EncodeSyntaxTree(doc *yaml.Node) ([]byte, error) {
	if doc == nil {
		return nil, fmt.Errorf("syntax tree is nil")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// UpsertMappingScalar sets key=val inside a YAML MappingNode, preserving existing
// comments and untouched keys.
func UpsertMappingScalar(mapping *yaml.Node, key, val string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Tag = "!!str"
			mapping.Content[i+1].Value = val
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val},
	)
}

// EnsureMappingChild returns or creates a MappingNode value for key inside mapping.
func EnsureMappingChild(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && mapping.Content[i+1].Kind == yaml.MappingNode {
			return mapping.Content[i+1]
		}
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child
}

// AppendSequenceScalar appends val to a sequence child key inside mapping if absent.
func AppendSequenceScalar(mapping *yaml.Node, key, val string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	var seq *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && mapping.Content[i+1].Kind == yaml.SequenceNode {
			seq = mapping.Content[i+1]
			break
		}
	}
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			seq,
		)
	}
	for _, item := range seq.Content {
		if item.Value == val {
			return
		}
	}
	seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val})
}

// SaveDefinitionAtomic validates nextBytes against schema + catalog, checks that
// the on-disk file still matches baseDigest (when baseDigest != ""), and writes atomically.
func SaveDefinitionAtomic(path, baseDigest string, nextBytes []byte, reg *catalog.Registry, loader WorkflowLoader) (SaveResult, error) {
	def, pDiags := ParseBytes(path, nextBytes)
	if len(pDiags) > 0 {
		return SaveResult{}, pDiags[0]
	}
	if _, cDiags := Compile(def, reg, loader); len(cDiags) > 0 {
		return SaveResult{}, cDiags[0]
	}

	if baseDigest != "" {
		onDisk, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return SaveResult{}, err
			}
		} else if DigestBytes(onDisk) != baseDigest {
			return SaveResult{}, ErrExternalEdit
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return SaveResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wf-*")
	if err != nil {
		return SaveResult{}, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(nextBytes); err != nil {
		_ = tmp.Close()
		return SaveResult{}, err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return SaveResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return SaveResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return SaveResult{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return SaveResult{}, err
	}
	cleanup = false

	return SaveResult{
		Digest:  DigestBytes(nextBytes),
		SavedAt: time.Now().UnixNano(),
	}, nil
}

// LineDiff produces a concise line-by-line diff preview between before and after YAML.
func LineDiff(before, after string) string {
	a := strings.Split(strings.TrimRight(before, "\n"), "\n")
	b := strings.Split(strings.TrimRight(after, "\n"), "\n")
	var out strings.Builder
	maxLen := max(len(a), len(b))
	for i := range maxLen {
		var la, lb string
		if i < len(a) {
			la = a[i]
		}
		if i < len(b) {
			lb = b[i]
		}
		if la == lb {
			out.WriteString("  " + la + "\n")
			continue
		}
		if i < len(a) {
			out.WriteString("- " + la + "\n")
		}
		if i < len(b) {
			out.WriteString("+ " + lb + "\n")
		}
	}
	return out.String()
}
