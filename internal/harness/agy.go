package harness

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type agyAdapter struct{}

var defaultAgyModes = []string{"accept-edits", "auto", "plan"}

func (agyAdapter) Kind() Kind { return KindAgy }

func (agyAdapter) Probe(ctx context.Context, binary string) (Capabilities, error) {
	path, err := resolveBinary(binary, "agy")
	if err != nil {
		return Capabilities{}, err
	}
	verOut, err := runProbeCommand(ctx, path, "--version")
	if err != nil {
		return Capabilities{}, fmt.Errorf("probe agy version: %w", err)
	}
	helpOut, _ := runProbeCommand(ctx, path, "--help")
	return Capabilities{
		Kind:                  KindAgy,
		BinaryPath:            path,
		Version:               strings.Split(verOut, "\n")[0],
		SupportsResume:        strings.Contains(helpOut, "--conversation") || strings.Contains(helpOut, "--resume"),
		SupportsInitialPrompt: true,
		SupportedModes:        append([]string(nil), defaultAgyModes...),
		SupportsCallbackEnv:   true,
	}, nil
}

func (agyAdapter) Validate(caps Capabilities, prof Profile) error {
	if prof.AgyMode != "" && !slices.Contains(caps.SupportedModes, prof.AgyMode) {
		return fmt.Errorf("%w: agy mode %q (supported: %v)", ErrUnsupportedMode, prof.AgyMode, caps.SupportedModes)
	}
	return nil
}

func (a agyAdapter) BuildLaunch(caps Capabilities, spec LaunchSpec) (ResolvedLaunch, error) {
	if err := a.Validate(caps, spec.Profile); err != nil {
		return ResolvedLaunch{}, err
	}
	resume, convID, linkedFrom, err := handleResumeFallback(spec)
	if err != nil {
		return ResolvedLaunch{}, err
	}

	argv := []string{caps.BinaryPath}
	if spec.Profile.AgyMode != "" {
		argv = append(argv, "--mode", spec.Profile.AgyMode)
	}
	if spec.Profile.Model != "" {
		argv = append(argv, "--model", spec.Profile.Model)
	}
	if resume {
		argv = append(argv, "--conversation", convID)
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

var agyConvRe = regexp.MustCompile(`(?i)(?:conversation[_ ]id|--conversation)\s*[:=]?\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[A-Za-z0-9_-]{8,})`)

func (agyAdapter) ParseConversationRef(output []byte) (string, string, bool) {
	m := agyConvRe.FindSubmatch(output)
	if len(m) < 2 {
		return "", "", false
	}
	id := string(m[1])
	return id, "agy:" + id, true
}
