// Package harness probes and launches interactive AI coding harnesses
// (Claude, Codex, agy) with versioned capabilities, independent policy modes,
// exact conversation resume, and durable transcript storage.
package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Kind identifies a supported harness CLI.
type Kind string

const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"
	KindAgy    Kind = "agy"
)

// ProfileSource records where an effective harness profile originated.
type ProfileSource string

const (
	SourceLaunchOverride ProfileSource = "launch_override"
	SourceWorkflowStep   ProfileSource = "workflow_step"
	SourceHarnessDefault ProfileSource = "harness_default"
	SourceBundledDefault ProfileSource = "bundled_default"
)

var (
	ErrMissingExecutable = errors.New("harness executable not found")
	ErrUnsupportedMode   = errors.New("unsupported harness mode")
	ErrResumeUnavailable = errors.New("resume conversation ID unavailable")
)

// Capabilities describes the probed abilities of an installed harness binary.
type Capabilities struct {
	Kind                  Kind     `json:"kind"`
	BinaryPath            string   `json:"binary_path"`
	Version               string   `json:"version"`
	SupportsResume        bool     `json:"supports_resume"`
	SupportsSessionID     bool     `json:"supports_session_id"`
	SupportsMCPConfig     bool     `json:"supports_mcp_config"`
	SupportsInitialPrompt bool     `json:"supports_initial_prompt"`
	SupportedModes        []string `json:"supported_modes,omitempty"`
	SupportedApprovals    []string `json:"supported_approvals,omitempty"`
	SupportedSandboxes    []string `json:"supported_sandboxes,omitempty"`
	SupportsCallbackEnv   bool     `json:"supports_callback_env"`
}

// Profile holds harness-specific policy settings. Claude permission mode,
// Codex approval+sandbox, and agy mode remain independent.
type Profile struct {
	Harness              Kind          `json:"harness"`
	Executable           string        `json:"executable,omitempty"`
	Model                string        `json:"model,omitempty"`
	Effort               string        `json:"effort,omitempty"`
	ClaudePermissionMode string        `json:"claude_permission_mode,omitempty"`
	CodexApprovalPolicy  string        `json:"codex_approval_policy,omitempty"`
	CodexSandboxMode     string        `json:"codex_sandbox_mode,omitempty"`
	AgyMode              string        `json:"agy_mode,omitempty"`
	Source               ProfileSource `json:"source"`
}

// LaunchSpec describes a single harness launch or resume request.
type LaunchSpec struct {
	Harness         Kind
	WorkDir         string
	Prompt          string
	ConversationID  string
	Resume          bool
	ResumeAvailable *bool // nil means unknown/assumed available; false triggers explicit fallback if FallbackNewChat is true
	FallbackNewChat bool
	CallbackEnv     map[string]string
	MCPConfigPath   string
	Profile         Profile
}

// ResolvedLaunch is the fully resolved command, environment, and metadata for a host.
type ResolvedLaunch struct {
	Argv                     []string     `json:"argv"`
	Env                      []string     `json:"env"`
	WorkDir                  string       `json:"work_dir"`
	Capabilities             Capabilities `json:"capabilities"`
	Profile                  Profile      `json:"profile"`
	ConversationID           string       `json:"conversation_id,omitempty"`
	LinkedFromConversationID string       `json:"linked_from_conversation_id,omitempty"`
	PTYPrompt                string       `json:"pty_prompt,omitempty"`
}

// Adapter probes, validates, and builds launches for one harness kind.
type Adapter interface {
	Kind() Kind
	Probe(ctx context.Context, binary string) (Capabilities, error)
	Validate(caps Capabilities, prof Profile) error
	BuildLaunch(caps Capabilities, spec LaunchSpec) (ResolvedLaunch, error)
	ParseConversationRef(output []byte) (conversationID, transcriptLocator string, ok bool)
}

// ForKind returns the adapter for kind.
func ForKind(kind Kind) (Adapter, error) {
	switch kind {
	case KindClaude:
		return claudeAdapter{}, nil
	case KindCodex:
		return codexAdapter{}, nil
	case KindAgy:
		return agyAdapter{}, nil
	default:
		return nil, fmt.Errorf("unknown harness kind %q", kind)
	}
}

func resolveBinary(override, defaultName string) (string, error) {
	target := defaultName
	if override != "" {
		target = override
	}
	path, err := exec.LookPath(target)
	if err != nil {
		return "", fmt.Errorf("%w: %s (%v)", ErrMissingExecutable, target, err)
	}
	return path, nil
}

func runProbeCommand(ctx context.Context, binary string, args ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func mergeCallbackEnv(base []string, extra map[string]string) []string {
	env := make([]string, 0, len(base)+len(extra)+1)
	env = append(env, base...)
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+extra[k])
	}
	return env
}

func handleResumeFallback(spec LaunchSpec) (resume bool, convID string, linkedFrom string, err error) {
	if !spec.Resume {
		return false, spec.ConversationID, "", nil
	}
	if spec.ConversationID == "" {
		return false, "", "", fmt.Errorf("%w: empty conversation id", ErrResumeUnavailable)
	}
	if spec.ResumeAvailable != nil && !*spec.ResumeAvailable {
		if !spec.FallbackNewChat {
			return false, "", "", fmt.Errorf("%w: %s", ErrResumeUnavailable, spec.ConversationID)
		}
		return false, "", spec.ConversationID, nil
	}
	return true, spec.ConversationID, "", nil
}

// Coverage classifies how a transcript was captured.
type Coverage string

const (
	CoverageStructured       Coverage = "structured"
	CoverageTerminalFallback Coverage = "terminal_fallback"
)

// Turn represents one readable conversation turn or fallback terminal excerpt.
type Turn struct {
	Role      string   `json:"role"`
	Text      string   `json:"text"`
	Coverage  Coverage `json:"coverage"`
	Hidden    bool     `json:"-"` // never persisted
	Timestamp int64    `json:"timestamp"`
}

// TranscriptMeta records harness identity and transcript coverage metadata.
type TranscriptMeta struct {
	Harness                  Kind     `json:"harness"`
	ConversationID           string   `json:"conversation_id,omitempty"`
	TranscriptLocator        string   `json:"transcript_locator,omitempty"`
	LinkedFromConversationID string   `json:"linked_from_conversation_id,omitempty"`
	Coverage                 Coverage `json:"coverage"`
	UpdatedAt                int64    `json:"updated_at"`
}

// TranscriptStore persists readable turns and sanitized terminal output in private storage.
type TranscriptStore struct {
	dir        string
	metaPath   string
	turnsPath  string
}

// NewTranscriptStore opens durable transcript storage under dir (mode 0700).
func NewTranscriptStore(dir string) (*TranscriptStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("transcript dir is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &TranscriptStore{
		dir:       dir,
		metaPath:  filepath.Join(dir, "transcript_meta.json"),
		turnsPath: filepath.Join(dir, "transcript.jsonl"),
	}, nil
}

// SaveMeta atomically persists transcript metadata.
func (s *TranscriptStore) SaveMeta(meta TranscriptMeta) error {
	meta.UpdatedAt = time.Now().UnixNano()
	if meta.Coverage == "" {
		meta.Coverage = CoverageStructured
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateAtomic(s.metaPath, data)
}

// AppendTurn appends a readable turn unless Hidden is true.
func (s *TranscriptStore) AppendTurn(turn Turn) error {
	if turn.Hidden {
		return nil
	}
	if turn.Timestamp == 0 {
		turn.Timestamp = time.Now().UnixNano()
	}
	if turn.Coverage == "" {
		turn.Coverage = CoverageStructured
	}
	line, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.turnsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// AppendTerminalOutput sanitizes raw VT output and records it with explicit
// terminal_fallback coverage.
func (s *TranscriptStore) AppendTerminalOutput(raw []byte) error {
	clean := SanitizeTerminalOutput(raw)
	if clean == "" {
		return nil
	}
	return s.AppendTurn(Turn{
		Role:     "terminal",
		Text:     clean,
		Coverage: CoverageTerminalFallback,
	})
}

// Read returns all persisted turns and metadata.
func (s *TranscriptStore) Read() ([]Turn, TranscriptMeta, error) {
	var meta TranscriptMeta
	if data, err := os.ReadFile(s.metaPath); err == nil {
		_ = json.Unmarshal(data, &meta)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, TranscriptMeta{}, err
	}

	f, err := os.Open(s.turnsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, meta, nil
		}
		return nil, TranscriptMeta{}, err
	}
	defer f.Close()

	var turns []Turn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var t Turn
		if err := json.Unmarshal(line, &t); err == nil {
			turns = append(turns, t)
		}
	}
	return turns, meta, scanner.Err()
}

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// SanitizeTerminalOutput strips ANSI/OSC escape sequences and non-printable control bytes.
func SanitizeTerminalOutput(raw []byte) string {
	stripped := ansiEscapeRe.ReplaceAll(raw, nil)
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range string(stripped) {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func writePrivateAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
