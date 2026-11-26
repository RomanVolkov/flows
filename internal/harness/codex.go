package harness

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type codexAdapter struct{}

var (
	defaultCodexApprovals = []string{"untrusted", "on-failure", "on-request", "never"}
	defaultCodexSandboxes = []string{"read-only", "workspace-write", "danger-full-access"}
)

func (codexAdapter) Kind() Kind { return KindCodex }

func (codexAdapter) Probe(ctx context.Context, binary string) (Capabilities, error) {
	path, err := resolveBinary(binary, "codex")
	if err != nil {
		return Capabilities{}, err
	}
	verOut, err := runProbeCommand(ctx, path, "--version")
	if err != nil {
		return Capabilities{}, fmt.Errorf("probe codex version: %w", err)
	}
	helpOut, _ := runProbeCommand(ctx, path, "--help")
	return Capabilities{
		Kind:                  KindCodex,
		BinaryPath:            path,
		Version:               strings.Split(verOut, "\n")[0],
		SupportsResume:        strings.Contains(helpOut, "resume"),
		SupportsInitialPrompt: true,
		SupportedApprovals:    append([]string(nil), defaultCodexApprovals...),
		SupportedSandboxes:    append([]string(nil), defaultCodexSandboxes...),
		SupportsCallbackEnv:   true,
	}, nil
}

func (codexAdapter) Validate(caps Capabilities, prof Profile) error {
	if prof.CodexApprovalPolicy != "" && !slices.Contains(caps.SupportedApprovals, prof.CodexApprovalPolicy) {
		return fmt.Errorf("%w: codex approval policy %q (supported: %v)", ErrUnsupportedMode, prof.CodexApprovalPolicy, caps.SupportedApprovals)
	}
	if prof.CodexSandboxMode != "" && !slices.Contains(caps.SupportedSandboxes, prof.CodexSandboxMode) {
		return fmt.Errorf("%w: codex sandbox mode %q (supported: %v)", ErrUnsupportedMode, prof.CodexSandboxMode, caps.SupportedSandboxes)
	}
	return nil
}

func (a codexAdapter) BuildLaunch(caps Capabilities, spec LaunchSpec) (ResolvedLaunch, error) {
	if err := a.Validate(caps, spec.Profile); err != nil {
		return ResolvedLaunch{}, err
	}
	resume, convID, linkedFrom, err := handleResumeFallback(spec)
	if err != nil {
		return ResolvedLaunch{}, err
	}

	argv := []string{caps.BinaryPath}
	if resume {
		argv = append(argv, "resume", convID)
	}
	if spec.Profile.CodexApprovalPolicy != "" {
		argv = append(argv, "-a", spec.Profile.CodexApprovalPolicy)
	}
	if spec.Profile.CodexSandboxMode != "" {
		argv = append(argv, "-s", spec.Profile.CodexSandboxMode)
	}
	if spec.Profile.Model != "" {
		argv = append(argv, "-m", spec.Profile.Model)
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

var codexSessionRe = regexp.MustCompile(`(?i)(?:codex resume|session id|thread_id)\s*[:=]?\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[A-Za-z0-9_-]{8,})`)

func (codexAdapter) ParseConversationRef(output []byte) (string, string, bool) {
	m := codexSessionRe.FindSubmatch(output)
	if len(m) < 2 {
		return "", "", false
	}
	id := string(m[1])
	return id, "codex:" + id, true
}
