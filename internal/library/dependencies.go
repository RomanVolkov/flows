package library

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
	"github.com/romanvolkov/flows/internal/workflow"
	"gopkg.in/yaml.v3"
)

type dependencyValidator struct {
	ctx      context.Context
	home     paths.Home
	settings config.Settings
	report   *ValidationReport
	checked  map[string]bool
}

func newDependencyValidator(ctx context.Context, home paths.Home, report *ValidationReport) *dependencyValidator {
	settings, err := config.LoadSettings(home.Settings())
	checker := &dependencyValidator{ctx: ctx, home: home, settings: settings, report: report, checked: make(map[string]bool)}
	if err != nil {
		checker.issue(report.Directory, "settings_unavailable", err.Error(), false)
	}
	return checker
}

func (c *dependencyValidator) issue(file, code, message string, warning bool) {
	severity := "error"
	if warning {
		severity = "warning"
	} else {
		c.report.Valid = false
	}
	c.report.Diagnostics = append(c.report.Diagnostics, workflow.Diagnostic{
		File: file, Line: 1, Column: 1, Code: code, Message: message, Severity: severity,
	})
}

func (c *dependencyValidator) first(key string) bool {
	if c.checked[key] {
		return false
	}
	c.checked[key] = true
	return true
}

func (c *dependencyValidator) workflow(def *workflow.Definition, reg *catalog.Registry, loader workflow.RelativeWorkflowLoader) {
	if !c.first("workflow:" + def.File) {
		return
	}
	defaults := make(map[string]any)
	for name, spec := range def.Inputs {
		if spec.Default != nil {
			defaults[name] = spec.Default
		}
	}
	bind := workflow.BindContext{Inputs: defaults}
	for _, id := range def.NodeOrder {
		if c.ctx.Err() != nil {
			c.issue(def.File, "dependency_check_timeout", "Dependency checks timed out", false)
			return
		}
		node := def.Nodes[id]
		if node.Workflow != "" {
			if child, err := loader(def, node.Workflow); err == nil {
				c.workflow(child, reg, loader)
			}
			continue
		}
		if name, ok := node.With["prompt_template"].(string); ok && name != "" && !strings.Contains(name, "${{") {
			path, err := workflow.ResolveTemplatePath(filepath.Dir(def.File), name)
			if err == nil {
				info, statErr := os.Stat(path)
				if statErr != nil || !info.Mode().IsRegular() {
					err = fmt.Errorf("prompt template is missing or unreadable")
				}
			}
			if err != nil {
				c.issue(def.File, "missing_prompt_template", fmt.Sprintf("Step %s: %s (%s)", id, err, name), false)
			}
		}
		switch node.Uses {
		case "core.command":
			value, err := workflow.ResolveValue(node.With["command"], bind)
			command, ok := value.(string)
			if err != nil {
				c.issue(def.File, "dependency_deferred", fmt.Sprintf("Step %s: check the executable with the actual run inputs", id), true)
			} else if !ok || command == "" {
				c.issue(def.File, "missing_executable", fmt.Sprintf("Step %s requires a command", id), false)
			} else if command != "flows" {
				c.executable(def.File, command)
			}
		case "core.harness":
			kind := c.settings.DefaultHarness
			if raw := node.With["harness"]; raw != nil {
				value, err := workflow.ResolveValue(raw, bind)
				if err != nil {
					c.issue(def.File, "dependency_deferred", fmt.Sprintf("Step %s: check the harness with the actual run inputs", id), true)
					continue
				}
				text, _ := value.(string)
				if text != "" {
					kind = harness.Kind(text)
				}
			}
			if !c.first("harness:" + string(kind)) {
				continue
			}
			adapter, err := harness.ForKind(kind)
			if err == nil {
				ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
				profile := config.ProfileFromSettings(kind, c.settings)
				caps, probeErr := adapter.Probe(ctx, profile.Executable)
				err = probeErr
				if err == nil {
					err = adapter.Validate(caps, profile)
				}
				cancel()
			}
			if err != nil {
				c.issue(def.File, "harness_unavailable", fmt.Sprintf("Harness %s is unavailable or misconfigured: %s", kind, err), false)
			}
		default:
			op, pkg, err := reg.Describe(node.Uses)
			if err == nil && pkg.PackageID != "core" {
				if op.ExecutionKind != catalog.ExecPlugin {
					c.issue(def.File, "missing_plugin_implementation", fmt.Sprintf("Operation %s needs a plugin implementation", node.Uses), false)
				}
				c.tool(pkg.SourcePath, pkg.PackageID, true)
			}
		}
	}
}

func (c *dependencyValidator) executable(file, name string) {
	if !c.first("executable:" + name) {
		return
	}
	if _, err := exec.LookPath(executableOverride(name)); err != nil {
		c.issue(file, "missing_executable", fmt.Sprintf("Required executable %s is unavailable on PATH", name), false)
	}
}

func executableOverride(name string) string {
	switch name {
	case "gh", "glab":
		if path := os.Getenv("FLOWS_" + strings.ToUpper(name) + "_BIN"); path != "" {
			return path
		}
	}
	return name
}

func (c *dependencyValidator) tool(dir, id string, installed bool) {
	if !c.first("tool:" + id) {
		return
	}
	path := filepath.Join(dir, "tool.yaml")
	raw, err := os.ReadFile(path)
	var manifest toolManifestYAML
	if err == nil {
		err = yaml.Unmarshal(raw, &manifest)
	}
	if err != nil {
		c.issue(path, "tool_manifest_unavailable", fmt.Sprintf("Can't read tool %s prerequisites: %s", id, err), false)
		return
	}
	for _, name := range manifest.RequiredCLIs {
		c.executable(path, name)
	}
	needsPlugin := false
	for _, op := range manifest.Operations {
		needsPlugin = needsPlugin || op.ExecutionKind == "plugin"
	}
	if needsPlugin {
		if installed {
			active, err := plugin.LoadActiveTool(c.home, id)
			if err == nil {
				_, err = exec.LookPath(active.Receipt.BinaryPath)
			}
			if err != nil {
				c.issue(path, "missing_plugin_build", fmt.Sprintf("Tool %s has no active executable build. Import it and wait for preparation to finish.", id), false)
			}
		} else if !hasExecutableBinary(filepath.Join(dir, id+"-plugin")) {
			if !hasGoSourceFiles(dir) {
				c.issue(path, "missing_plugin_implementation", fmt.Sprintf("Tool %s declares plugin operations but has no Go source or executable", id), false)
			} else {
				ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
				_, _, err := plugin.CheckGoCompiler(ctx, "")
				cancel()
				if err != nil {
					c.issue(path, "missing_go_compiler", fmt.Sprintf("Tool %s needs the Go compiler: %s", id, err), false)
				}
			}
		}
	}
	for _, credential := range manifest.Credentials {
		if credential.Required && c.first("credential:"+credential.Provider+":"+credential.ID) {
			c.credential(path, credential.Provider, credential.ID)
		}
	}
}

func (c *dependencyValidator) credential(file, provider, id string) {
	var command string
	var args, vars []string
	switch provider {
	case "github":
		command, args = "gh", []string{"auth", "status", "--hostname", "github.com"}
	case "gitlab":
		command, args = "glab", []string{"auth", "status"}
	case "jira":
		vars = []string{"FLOWS_JIRA_URL", "FLOWS_JIRA_TOKEN"}
	case "youtrack":
		vars = []string{"FLOWS_YOUTRACK_URL", "FLOWS_YOUTRACK_TOKEN"}
	case "zulip":
		vars = []string{"FLOWS_ZULIP_URL", "FLOWS_ZULIP_EMAIL", "FLOWS_ZULIP_API_KEY"}
	default:
		if c.settings.Credentials[id] == "" {
			c.issue(file, "missing_credential", fmt.Sprintf("Configure credential %s for %s", id, provider), false)
		} else {
			c.issue(file, "dependency_deferred", fmt.Sprintf("Credential %s is configured. Verify its access with a read-only request.", id), true)
		}
		return
	}
	for _, name := range vars {
		if os.Getenv(name) == "" {
			c.issue(file, "missing_credential", fmt.Sprintf("Set %s for %s", name, provider), false)
		}
	}
	if command != "" {
		path, err := exec.LookPath(executableOverride(command))
		if err != nil {
			c.executable(file, command)
			return
		}
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, path, args...).Run(); err != nil {
			c.issue(file, "credential_unavailable", fmt.Sprintf("Could not verify %s authentication. Run %s auth status to check credentials and connectivity.", provider, command), false)
		}
	}
}
