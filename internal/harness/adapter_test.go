package harness

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/host"
)

func writeFixtureCLI(t *testing.T, dir, name, version, help string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo \"" + version + "\"; exit 0; fi\n" +
		"if [ \"$1\" = \"--help\" ]; then cat <<'EOF'\n" + help + "\nEOF\nexit 0; fi\n" +
		"echo \"ARGV:$*\"\n" +
		"echo \"CONV_ID:conv-12345678\"\n" +
		"while IFS= read -r line; do echo \"REPLY:$line\"; [ \"$line\" = \"exit\" ] && break; done\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeMissingAndUnsupportedModes(t *testing.T) {
	ctx := context.Background()

	for _, kind := range []Kind{KindClaude, KindCodex, KindAgy} {
		adp, err := ForKind(kind)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adp.Probe(ctx, filepath.Join(t.TempDir(), "missing-"+string(kind))); !errors.Is(err, ErrMissingExecutable) {
			t.Fatalf("%s missing binary: got %v, want ErrMissingExecutable", kind, err)
		}
	}

	binDir := t.TempDir()
	claudeBin := writeFixtureCLI(t, binDir, "claude", "2.1.311 (Claude Code)", "--permission-mode --resume --session-id --mcp-config")
	codexBin := writeFixtureCLI(t, binDir, "codex", "codex-cli 0.172.0", "resume --ask-for-approval --sandbox")
	agyBin := writeFixtureCLI(t, binDir, "agy", "1.5.18", "--mode --conversation --resume")

	cAdp, _ := ForKind(KindClaude)
	cCaps, err := cAdp.Probe(ctx, claudeBin)
	if err != nil || !cCaps.SupportsResume || !cCaps.SupportsSessionID || !cCaps.SupportsMCPConfig {
		t.Fatalf("claude probe failed: caps=%+v err=%v", cCaps, err)
	}
	if err := cAdp.Validate(cCaps, Profile{Harness: KindClaude, ClaudePermissionMode: "bogus"}); !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("expected ErrUnsupportedMode for claude, got %v", err)
	}

	xAdp, _ := ForKind(KindCodex)
	xCaps, err := xAdp.Probe(ctx, codexBin)
	if err != nil || !xCaps.SupportsResume {
		t.Fatalf("codex probe failed: caps=%+v err=%v", xCaps, err)
	}
	if err := xAdp.Validate(xCaps, Profile{Harness: KindCodex, CodexApprovalPolicy: "bogus"}); !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("expected ErrUnsupportedMode for codex approval, got %v", err)
	}
	if err := xAdp.Validate(xCaps, Profile{Harness: KindCodex, CodexSandboxMode: "bogus"}); !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("expected ErrUnsupportedMode for codex sandbox, got %v", err)
	}

	aAdp, _ := ForKind(KindAgy)
	aCaps, err := aAdp.Probe(ctx, agyBin)
	if err != nil || !aCaps.SupportsResume {
		t.Fatalf("agy probe failed: caps=%+v err=%v", aCaps, err)
	}
	if err := aAdp.Validate(aCaps, Profile{Harness: KindAgy, AgyMode: "bogus"}); !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("expected ErrUnsupportedMode for agy, got %v", err)
	}
}

func TestBuildLaunchEmptyPromptResumeAndFallback(t *testing.T) {
	ctx := context.Background()
	binDir := t.TempDir()
	workDir := t.TempDir()

	claudeBin := writeFixtureCLI(t, binDir, "claude", "2.1.311", "--permission-mode --resume --session-id --mcp-config")
	codexBin := writeFixtureCLI(t, binDir, "codex", "codex-cli 0.172.0", "resume --ask-for-approval --sandbox")
	agyBin := writeFixtureCLI(t, binDir, "agy", "1.5.18", "--mode --conversation")

	cAdp, _ := ForKind(KindClaude)
	cCaps, _ := cAdp.Probe(ctx, claudeBin)
	xAdp, _ := ForKind(KindCodex)
	xCaps, _ := xAdp.Probe(ctx, codexBin)
	aAdp, _ := ForKind(KindAgy)
	aCaps, _ := aAdp.Probe(ctx, agyBin)

	// 1. Empty session + initial prompt session for each harness.
	for _, tc := range []struct {
		adp  Adapter
		caps Capabilities
		prof Profile
		flag string
	}{
		{cAdp, cCaps, Profile{Harness: KindClaude, ClaudePermissionMode: "plan", Source: SourceLaunchOverride}, "--permission-mode"},
		{xAdp, xCaps, Profile{Harness: KindCodex, CodexApprovalPolicy: "on-request", CodexSandboxMode: "workspace-write", Source: SourceWorkflowStep}, "-a"},
		{aAdp, aCaps, Profile{Harness: KindAgy, AgyMode: "accept-edits", Source: SourceHarnessDefault}, "--mode"},
	} {
		empty, err := tc.adp.BuildLaunch(tc.caps, LaunchSpec{
			Harness: tc.caps.Kind,
			WorkDir: workDir,
			Profile: tc.prof,
		})
		if err != nil {
			t.Fatalf("%s empty launch: %v", tc.caps.Kind, err)
		}
		if !slices.Contains(empty.Argv, tc.flag) {
			t.Fatalf("%s empty argv missing %s: %v", tc.caps.Kind, tc.flag, empty.Argv)
		}

		withPrompt, err := tc.adp.BuildLaunch(tc.caps, LaunchSpec{
			Harness: tc.caps.Kind,
			WorkDir: workDir,
			Prompt:  "fix failing test",
			Profile: tc.prof,
			CallbackEnv: map[string]string{
				"FLOWS_ATTEMPT_ID": "att-1",
			},
		})
		if err != nil {
			t.Fatalf("%s prompt launch: %v", tc.caps.Kind, err)
		}
		if withPrompt.Argv[len(withPrompt.Argv)-1] != "fix failing test" {
			t.Fatalf("%s expected prompt at end of argv, got %v", tc.caps.Kind, withPrompt.Argv)
		}
		if !slices.Contains(withPrompt.Env, "FLOWS_ATTEMPT_ID=att-1") {
			t.Fatalf("%s missing callback env", tc.caps.Kind)
		}
	}

	// 2. Resume two distinct conversations in the same directory.
	r1, err := cAdp.BuildLaunch(cCaps, LaunchSpec{Harness: KindClaude, WorkDir: workDir, ConversationID: "conv-A", Resume: true})
	if err != nil || !slices.Contains(r1.Argv, "--resume") || !slices.Contains(r1.Argv, "conv-A") {
		t.Fatalf("resume conv-A: err=%v argv=%v", err, r1.Argv)
	}
	r2, err := cAdp.BuildLaunch(cCaps, LaunchSpec{Harness: KindClaude, WorkDir: workDir, ConversationID: "conv-B", Resume: true})
	if err != nil || !slices.Contains(r2.Argv, "conv-B") {
		t.Fatalf("resume conv-B: err=%v argv=%v", err, r2.Argv)
	}

	// 3. Unavailable resume ID with FallbackNewChat links old conversation explicitly.
	unavailable := false
	fb, err := aAdp.BuildLaunch(aCaps, LaunchSpec{
		Harness:         KindAgy,
		WorkDir:         workDir,
		ConversationID:  "conv-missing",
		Resume:          true,
		ResumeAvailable: &unavailable,
		FallbackNewChat: true,
	})
	if err != nil {
		t.Fatalf("fallback launch: %v", err)
	}
	if fb.LinkedFromConversationID != "conv-missing" || fb.ConversationID != "" {
		t.Fatalf("expected linked_from=conv-missing and empty convID, got %+v", fb)
	}
}

func TestTranscriptStoreAndInteractiveHostReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Transcript store: structured turns, sanitized terminal fallback, hidden input excluded.
	ts, err := NewTranscriptStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.SaveMeta(TranscriptMeta{
		Harness:           KindAgy,
		ConversationID:    "conv-xyz",
		TranscriptLocator: "agy:conv-xyz",
		Coverage:          CoverageStructured,
	}); err != nil {
		t.Fatal(err)
	}
	if err := ts.AppendTurn(Turn{Role: "user", Text: "secret-password", Hidden: true}); err != nil {
		t.Fatal(err)
	}
	if err := ts.AppendTurn(Turn{Role: "user", Text: "check status"}); err != nil {
		t.Fatal(err)
	}
	if err := ts.AppendTerminalOutput([]byte("\x1b[32mPASS\x1b[0m\r\n")); err != nil {
		t.Fatal(err)
	}
	turns, meta, err := ts.Read()
	if err != nil {
		t.Fatal(err)
	}
	if meta.ConversationID != "conv-xyz" || len(turns) != 2 {
		t.Fatalf("unexpected transcript: meta=%+v turns=%+v", meta, turns)
	}
	if turns[1].Coverage != CoverageTerminalFallback || turns[1].Text != "PASS" {
		t.Fatalf("unexpected fallback turn: %+v", turns[1])
	}

	// 2. Fixture interactive launch in host, kill host, reopen exchange, continue in fresh host.
	binDir := t.TempDir()
	agyBin := writeFixtureCLI(t, binDir, "agy", "1.5.18", "--mode --conversation")
	aAdp, _ := ForKind(KindAgy)
	aCaps, _ := aAdp.Probe(ctx, agyBin)

	launch, err := aAdp.BuildLaunch(aCaps, LaunchSpec{
		Harness: KindAgy,
		WorkDir: t.TempDir(),
		Prompt:  "hello",
		Profile: Profile{Harness: KindAgy, AgyMode: "auto", Source: SourceLaunchOverride},
	})
	if err != nil {
		t.Fatal(err)
	}

	sockDir, err := os.MkdirTemp("/tmp", "fh-har-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	h, err := host.Start(ctx, host.Config{
		SessionID:  "sess-har-1",
		AttemptID:  "att-har-1",
		StateDir:   t.TempDir(),
		SocketPath: filepath.Join(sockDir, "h1.sock"),
		WorkDir:    launch.WorkDir,
		Command:    launch.Argv[0],
		Args:       launch.Argv[1:],
		Env:        launch.Env,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := host.DialAttach(ctx, filepath.Join(sockDir, "h1.sock"), "sess-har-1", host.AttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteInput(ctx, []byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for {
		f, err := c.ReadFrame(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if f.Kind == host.FrameOutput {
			out.Write(f.Payload)
			if strings.Contains(out.String(), "REPLY:ping") {
				break
			}
		}
	}
	c.Close()
	_ = h.RequestStop(300 * time.Millisecond)
	_ = h.Close()

	// Resume in a fresh host using exact conversation ID.
	resumeLaunch, err := aAdp.BuildLaunch(aCaps, LaunchSpec{
		Harness:        KindAgy,
		WorkDir:        launch.WorkDir,
		ConversationID: "conv-12345678",
		Resume:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resumeLaunch.Argv, "--conversation") || !slices.Contains(resumeLaunch.Argv, "conv-12345678") {
		t.Fatalf("expected resume args, got %v", resumeLaunch.Argv)
	}
}
