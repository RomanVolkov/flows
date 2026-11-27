package localapi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/romanvolkov/flows/internal/paths"
)

func agyUsageDir(home paths.Home, attempt string) string {
	return filepath.Join(home.State, "agy-usage", safeID(attempt))
}

// agyUsageLaunch records the active conversation ID using Antigravity's status
// line protocol. The override applies only to this launch.
func (m *RunManager) agyUsageLaunch(attempt string, args, env []string) ([]string, []string) {
	cli, err := exec.LookPath(workflowCLIPath())
	if err != nil {
		return args, env
	}
	dir := agyUsageDir(m.home, attempt)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return args, env
	}
	command := "'" + strings.ReplaceAll(cli, "'", "'\"'\"'") + "' usage agy-link"
	status := map[string]any{"type": "command", "command": command, "enabled": true, "stack_with_default": true}
	// Keep an existing custom status line's output and layout. Only the helper
	// consumes a copy of stdin, while the original command still gets the JSON.
	settingsPath := filepath.Join(m.home.Environment()["HOME"], ".gemini", "antigravity-cli", "settings.json")
	var settings struct {
		Status map[string]any `json:"statusLine"`
	}
	if raw, err := os.ReadFile(settingsPath); err == nil && json.Unmarshal(raw, &settings) == nil {
		custom, _ := settings.Status["command"].(string)
		if custom != "" && settings.Status["enabled"] != false {
			escaped := "'" + strings.ReplaceAll(custom, "'", "'\"'\"'") + "'"
			status = settings.Status
			status["command"] = "data=$(cat); printf '%s' \"$data\" | " + command + "; printf '%s' \"$data\" | /bin/sh -c " + escaped
		}
	}
	override, _ := json.Marshal(map[string]any{"statusLine": status})
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, value := range env {
		if !strings.HasPrefix(value, "FLOWS_AGY_USAGE_DIR=") {
			out = append(out, value)
		}
	}
	return append([]string{"--settings", string(override)}, args...), append(out, "FLOWS_AGY_USAGE_DIR="+dir)
}
