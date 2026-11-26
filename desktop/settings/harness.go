// Package settings implements the desktop Settings panes for Harnesses,
// Tools & Operations Inventory, and General/Terminal configuration.
package settings

import (
	"context"

	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
)

// HarnessEntry models one harness card in Settings -> Harnesses.
type HarnessEntry struct {
	Kind               harness.Kind         `json:"kind"`
	Executable         string               `json:"executable"`
	ResolvedPath       string               `json:"resolved_path,omitempty"`
	Version            string               `json:"version,omitempty"`
	Available          bool                 `json:"available"`
	ValidationError    string               `json:"validation_error,omitempty"`
	SupportedModes     []string             `json:"supported_modes,omitempty"`
	SupportedApprovals []string             `json:"supported_approvals,omitempty"`
	SupportedSandboxes []string             `json:"supported_sandboxes,omitempty"`
	Profile            harness.Profile      `json:"profile"`
	DefaultProfile     harness.Profile      `json:"default_profile"`
	LaunchPreview      []string             `json:"launch_preview,omitempty"`
}

// HarnessPane displays Claude, Codex, and agy separately with live probing.
type HarnessPane struct {
	Entries map[harness.Kind]*HarnessEntry `json:"entries"`
}

// NewHarnessPane initializes separate entries for Claude, Codex, and agy from s.
func NewHarnessPane(ctx context.Context, s config.Settings) *HarnessPane {
	pane := &HarnessPane{
		Entries: make(map[harness.Kind]*HarnessEntry, 3),
	}
	for _, kind := range []harness.Kind{harness.KindClaude, harness.KindCodex, harness.KindAgy} {
		prof := config.ProfileFromSettings(kind, s)
		entry := &HarnessEntry{
			Kind:           kind,
			Executable:     prof.Executable,
			Profile:        prof,
			DefaultProfile: config.BundledDefaultProfile(kind),
		}
		pane.Entries[kind] = entry
		pane.Probe(ctx, kind)
	}
	return pane
}

// Probe inspects the configured executable for kind and updates supported modes
// and launch preview.
func (p *HarnessPane) Probe(ctx context.Context, kind harness.Kind) {
	entry, ok := p.Entries[kind]
	if !ok {
		return
	}
	adp, err := harness.ForKind(kind)
	if err != nil {
		entry.Available = false
		entry.ValidationError = err.Error()
		return
	}
	caps, err := adp.Probe(ctx, entry.Executable)
	if err != nil {
		entry.Available = false
		entry.ValidationError = err.Error()
		entry.ResolvedPath = ""
		entry.Version = ""
		entry.LaunchPreview = nil
		return
	}
	entry.Available = true
	entry.ValidationError = ""
	entry.ResolvedPath = caps.BinaryPath
	entry.Version = caps.Version
	entry.SupportedModes = caps.SupportedModes
	entry.SupportedApprovals = caps.SupportedApprovals
	entry.SupportedSandboxes = caps.SupportedSandboxes

	if err := adp.Validate(caps, entry.Profile); err != nil {
		entry.ValidationError = err.Error()
		return
	}
	launch, err := adp.BuildLaunch(caps, harness.LaunchSpec{
		Harness: kind,
		Profile: entry.Profile,
	})
	if err == nil {
		entry.LaunchPreview = launch.Argv
	}
}

// SetExecutable changes the executable path for kind and re-probes allowed modes.
func (p *HarnessPane) SetExecutable(ctx context.Context, kind harness.Kind, executable string) {
	entry, ok := p.Entries[kind]
	if !ok {
		return
	}
	entry.Executable = executable
	entry.Profile.Executable = executable
	p.Probe(ctx, kind)
}
