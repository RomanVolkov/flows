package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// RenderedPrompt holds both the runtime prompt and its redacted durable record.
type RenderedPrompt struct {
	TemplatePath string         `json:"template_path"`
	Text         string         `json:"-"`
	RedactedText string         `json:"redacted_text"`
	Arguments    map[string]any `json:"arguments,omitempty"`
}

// ResolveTemplatePath validates that relPath stays inside pkgDir and returns its clean path.
func ResolveTemplatePath(pkgDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("template path is empty")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("template path must be relative: %q", relPath)
	}
	clean := filepath.Clean(relPath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template path escapes package: %q", relPath)
	}
	return filepath.Join(pkgDir, clean), nil
}

// RenderPrompt deterministically substitutes named arguments in templateContent
// and produces a credential-redacted copy for durable attempt provenance.
func RenderPrompt(templatePath, templateContent string, args map[string]any, secrets []string) (RenderedPrompt, error) {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	rendered := templateContent
	redactedArgs := make(map[string]any, len(args))
	for _, k := range keys {
		valStr := formatArgValue(args[k])
		rendered = strings.ReplaceAll(rendered, "${{ arguments."+k+" }}", valStr)
		rendered = strings.ReplaceAll(rendered, "{{."+k+"}}", valStr)
		rendered = strings.ReplaceAll(rendered, "{{ "+k+" }}", valStr)
		redactedArgs[k] = Redact(valStr, secrets)
	}

	return RenderedPrompt{
		TemplatePath: templatePath,
		Text:         rendered,
		RedactedText: Redact(rendered, secrets),
		Arguments:    redactedArgs,
	}, nil
}

// StoreAttemptPrompt persists the redacted prompt and provenance in attemptDir.
func StoreAttemptPrompt(attemptDir string, rp RenderedPrompt) error {
	if err := os.MkdirAll(attemptDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(attemptDir, "prompt.json"), data, 0600)
}

func formatArgValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}
