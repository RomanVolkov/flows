// Package launcher implements the Cmd+N session launcher and run dispatch flow.
package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/romanvolkov/flows/desktop/inspector"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/localapi"
)

// SessionLauncher models the Cmd+N modal for launching an empty or prompted harness session.
type SessionLauncher struct {
	Open            bool
	Harness         harness.Kind
	Prompt          string
	WorkDir         string
	Profile         harness.Profile
	OpenImmediately bool
	RequestID       string
	Command         string
	Args            []string
}

// OpenNew triggers the Cmd+N session launcher with defaults.
func OpenNew(defaultDir string, defaultHarness harness.Kind) *SessionLauncher {
	if defaultHarness == "" {
		defaultHarness = harness.KindClaude
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &SessionLauncher{
		Open:            true,
		Harness:         defaultHarness,
		WorkDir:         defaultDir,
		Profile:         harness.Profile{Harness: defaultHarness, Source: harness.SourceLaunchOverride},
		OpenImmediately: true,
		RequestID:       "req-" + hex.EncodeToString(b),
	}
}

// Submit commits and starts the session via RunManager, selects the new root,
// and optionally opens the terminal dock immediately.
func (l *SessionLauncher) Submit(ctx context.Context, runs *localapi.RunManager, shell *inspector.ShellState) (localapi.CreateRunResponse, error) {
	resp, err := runs.LaunchSession(ctx, localapi.CreateRunRequest{
		RequestID: l.RequestID,
		Harness:   l.Harness,
		Prompt:    l.Prompt,
		WorkDir:   l.WorkDir,
		Profile:   l.Profile,
		Command:   l.Command,
		Args:      l.Args,
	})
	if err != nil {
		return localapi.CreateRunResponse{}, err
	}
	l.Open = false
	if shell != nil {
		root := inspector.EmptySessionRoot(resp.RunID, resp.SessionID, string(l.Harness))
		root.ID = resp.NodeID
		shell.Selection = []inspector.NodeSummary{root}
		if l.OpenImmediately {
			shell.OpenDock(resp.SessionID, resp.SocketPath)
		}
	}
	return resp, nil
}
