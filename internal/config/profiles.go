package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/harness"
)

var (
	validClaudeModes    = []string{"default", "acceptEdits", "plan", "dontAsk", "bypassPermissions"}
	validCodexApprovals = []string{"untrusted", "on-failure", "on-request", "never"}
	validCodexSandboxes = []string{"read-only", "workspace-write", "danger-full-access"}
	validAgyModes       = []string{"accept-edits", "auto", "plan"}
)

// ValidateSettings checks semantic validity of settings and rejects
// unsupported mode combinations or raw secret values in credential fields.
func ValidateSettings(s Settings) error {
	if s.Version != 0 && s.Version != SettingsVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidSettings, s.Version)
	}
	switch s.DefaultHarness {
	case harness.KindClaude, harness.KindCodex, harness.KindAgy:
	default:
		return fmt.Errorf("%w: unsupported default harness %q", ErrInvalidSettings, s.DefaultHarness)
	}
	if s.Concurrency <= 0 || s.Concurrency > 64 {
		return fmt.Errorf("%w: concurrency must be between 1 and 64", ErrInvalidSettings)
	}
	if s.Terminal.FontSize < 8 || s.Terminal.FontSize > 48 {
		return fmt.Errorf("%w: terminal font_size %d out of range [8, 48]", ErrInvalidSettings, s.Terminal.FontSize)
	}
	if s.Terminal.Opacity <= 0 || s.Terminal.Opacity > 1.0 {
		return fmt.Errorf("%w: terminal opacity %.2f out of range (0, 1]", ErrInvalidSettings, s.Terminal.Opacity)
	}

	for kind, hs := range s.Harnesses {
		switch kind {
		case harness.KindClaude:
			if hs.ClaudePermissionMode != "" && !slices.Contains(validClaudeModes, hs.ClaudePermissionMode) {
				return fmt.Errorf("%w: unsupported claude permission mode %q", ErrInvalidSettings, hs.ClaudePermissionMode)
			}
		case harness.KindCodex:
			if hs.CodexApprovalPolicy != "" && !slices.Contains(validCodexApprovals, hs.CodexApprovalPolicy) {
				return fmt.Errorf("%w: unsupported codex approval policy %q", ErrInvalidSettings, hs.CodexApprovalPolicy)
			}
			if hs.CodexSandboxMode != "" && !slices.Contains(validCodexSandboxes, hs.CodexSandboxMode) {
				return fmt.Errorf("%w: unsupported codex sandbox mode %q", ErrInvalidSettings, hs.CodexSandboxMode)
			}
		case harness.KindAgy:
			if hs.AgyMode != "" && !slices.Contains(validAgyModes, hs.AgyMode) {
				return fmt.Errorf("%w: unsupported agy mode %q", ErrInvalidSettings, hs.AgyMode)
			}
		default:
			return fmt.Errorf("%w: unknown harness %q", ErrInvalidSettings, kind)
		}
	}

	for name, ref := range s.Credentials {
		if ref == "" {
			continue
		}
		if !strings.HasPrefix(ref, "env:") && !strings.HasPrefix(ref, "keychain:") && !strings.HasPrefix(ref, "ref:") {
			return fmt.Errorf("%w: credential %q must store a reference (env:, keychain:, or ref:), not a raw secret", ErrInvalidSettings, name)
		}
	}
	return nil
}

// BundledDefaultProfile returns the built-in fallback profile for kind.
func BundledDefaultProfile(kind harness.Kind) harness.Profile {
	p := harness.Profile{
		Harness: kind,
		Source:  harness.SourceBundledDefault,
	}
	switch kind {
	case harness.KindClaude:
		p.ClaudePermissionMode = "default"
	case harness.KindCodex:
		p.CodexApprovalPolicy = "on-request"
		p.CodexSandboxMode = "workspace-write"
	case harness.KindAgy:
		p.AgyMode = "accept-edits"
	}
	return p
}

// ProfileFromSettings converts a HarnessSettings entry into a harness.Profile.
func ProfileFromSettings(kind harness.Kind, s Settings) harness.Profile {
	hs := s.Harnesses[kind]
	return harness.Profile{
		Harness:              kind,
		Executable:           hs.Executable,
		Model:                hs.Model,
		Effort:               hs.Effort,
		ClaudePermissionMode: hs.ClaudePermissionMode,
		CodexApprovalPolicy:  hs.CodexApprovalPolicy,
		CodexSandboxMode:     hs.CodexSandboxMode,
		AgyMode:              hs.AgyMode,
		Source:               harness.SourceHarnessDefault,
	}
}

// ResolveEffectiveProfile applies precedence:
// launchOverride -> stepOrWorkflow -> perHarness -> bundled default.
func ResolveEffectiveProfile(kind harness.Kind, launchOverride, stepOrWorkflow, perHarness *harness.Profile) harness.Profile {
	eff := BundledDefaultProfile(kind)
	apply := func(p *harness.Profile, src harness.ProfileSource) {
		if p == nil {
			return
		}
		changed := false
		if p.Executable != "" {
			eff.Executable = p.Executable
			changed = true
		}
		if p.Model != "" {
			eff.Model = p.Model
			changed = true
		}
		if p.Effort != "" {
			eff.Effort = p.Effort
			changed = true
		}
		if p.ClaudePermissionMode != "" {
			eff.ClaudePermissionMode = p.ClaudePermissionMode
			changed = true
		}
		if p.CodexApprovalPolicy != "" {
			eff.CodexApprovalPolicy = p.CodexApprovalPolicy
			changed = true
		}
		if p.CodexSandboxMode != "" {
			eff.CodexSandboxMode = p.CodexSandboxMode
			changed = true
		}
		if p.AgyMode != "" {
			eff.AgyMode = p.AgyMode
			changed = true
		}
		if changed {
			eff.Source = src
		}
	}
	apply(perHarness, harness.SourceHarnessDefault)
	apply(stepOrWorkflow, harness.SourceWorkflowStep)
	apply(launchOverride, harness.SourceLaunchOverride)
	return eff
}

// PinnedLaunchPolicy records the effective profile and executable version at launch.
type PinnedLaunchPolicy struct {
	Profile           harness.Profile `json:"profile"`
	ExecutableVersion string          `json:"executable_version"`
	PinnedAt          int64           `json:"pinned_at"`
}

// PersistLaunchPolicy writes the pinned launch policy to attemptDir/policy.json.
func PersistLaunchPolicy(attemptDir string, prof harness.Profile, execVersion string) error {
	if err := os.MkdirAll(attemptDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(PinnedLaunchPolicy{
		Profile:           prof,
		ExecutableVersion: execVersion,
		PinnedAt:          time.Now().UnixNano(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(attemptDir, "policy.json"), data, 0600)
}

// LoadLaunchPolicy reads a session's pinned launch policy from attemptDir.
func LoadLaunchPolicy(attemptDir string) (PinnedLaunchPolicy, error) {
	data, err := os.ReadFile(filepath.Join(attemptDir, "policy.json"))
	if err != nil {
		return PinnedLaunchPolicy{}, err
	}
	var p PinnedLaunchPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return PinnedLaunchPolicy{}, err
	}
	return p, nil
}
