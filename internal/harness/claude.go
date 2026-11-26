package harness

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type claudeAdapter struct{}

var defaultClaudeModes = []string{"default", "acceptEdits", "plan", "dontAsk", "bypassPermissions"}

func (claudeAdapter) Kind() Kind { return KindClaude }

func (claudeAdapter) Probe(ctx context.Context, binary string) (Capabilities, error) {
	path, err := resolveBinary(binary, "claude")
	if err != nil {
		return Capabilities{}, err
	}
	verOut, err := runProbeCommand(ctx, path, "--version")
	if err != nil {
		return Capabilities{}, fmt.Errorf("probe claude version: %w", err)
	}
	helpOut, _ := runProbeCommand(ctx, path, "--help")
	return Capabilities{
		Kind:                  KindClaude,
		BinaryPath:            path,
		Version:               strings.Split(verOut, "\n")[0],
		SupportsResume:        strings.Contains(helpOut, "--resume"),
		SupportsSessionID:     strings.Contains(helpOut, "--session-id"),
		SupportsMCPConfig:     strings.Contains(helpOut, "--mcp-config"),
		SupportsInitialPrompt: true,
		SupportedModes:        append([]string(nil), defaultClaudeModes...),
		SupportsCallbackEnv:   true,
	}, nil
}

func (claudeAdapter) Validate(caps Capabilities, prof Profile) error {
	if prof.ClaudePermissionMode != "" && !slices.Contains(caps.SupportedModes, prof.ClaudePermissionMode) {
		return fmt.Errorf("%w: claude permission mode %q (supported: %v)", ErrUnsupportedMode, prof.ClaudePermissionMode, caps.SupportedModes)
	}
	return nil
}

func (a claudeAdapter) BuildLaunch(caps Capabilities, spec LaunchSpec) (ResolvedLaunch, error) {
	if err := a.Validate(caps, spec.Profile); err != nil {
		return ResolvedLaunch{}, err
	}
	resume, convID, linkedFrom, err := handleResumeFallback(spec)
	if err != nil {
		return ResolvedLaunch{}, err
	}

	argv := []string{caps.BinaryPath}
	if spec.Profile.ClaudePermissionMode != "" {
		argv = append(argv, "--permission-mode", spec.Profile.ClaudePermissionMode)
	}
	if spec.Profile.Model != "" {
		argv = append(argv, "--model", spec.Profile.Model)
	}
	if spec.MCPConfigPath != "" && caps.SupportsMCPConfig {
		argv = append(argv, "--mcp-config", spec.MCPConfigPath)
	}
	if resume {
		argv = append(argv, "--resume", convID)
	} else if convID != "" && caps.SupportsSessionID {
		argv = append(argv, "--session-id", convID)
	}
	if spec.Prompt != "" {
		argv = append(argv, spec.Prompt)
	}

	return ResolvedLaunch{
		Argv:                     argv,
		Env:                      mergeCallbackEnv(os.Environ(), spec.CallbackEnv),
		WorkDir:                  spec.WorkDir,
		Capabilities:             caps,
		Profile:                  spec.Profile,
		ConversationID:           convID,
		LinkedFromConversationID: linkedFrom,
	}, nil
}

var claudeSessionRe = regexp.MustCompile(`(?i)(?:session[_ ]id|claude --resume)\s*[:=]?\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[A-Za-z0-9_-]{8,})`)

func (claudeAdapter) ParseConversationRef(output []byte) (string, string, bool) {
	m := claudeSessionRe.FindSubmatch(output)
	if len(m) < 2 {
		return "", "", false
	}
	id := string(m[1])
	return id, "claude:" + id, true
}
