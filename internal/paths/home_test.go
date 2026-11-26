package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	userHome := "/Users/" + strings.Repeat("long home ", 15)
	for _, tc := range []struct {
		name, xdg, override, config, state, cache, logs string
	}{
		{"default", "", "", userHome + "/.config/flows", userHome + "/Library/Application Support/Flows", userHome + "/Library/Caches/Flows", userHome + "/Library/Logs/Flows"},
		{"xdg", "/custom config", "", "/custom config/flows", userHome + "/Library/Application Support/Flows", userHome + "/Library/Caches/Flows", userHome + "/Library/Logs/Flows"},
		{"override", "/custom config", "/isolated home", "/isolated home/config", "/isolated home/state", "/isolated home/cache", "/isolated home/logs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(userHome, tc.xdg, tc.override, 501)
			if err != nil {
				t.Fatal(err)
			}
			if got.Config != tc.config || got.State != tc.state || got.Cache != tc.cache || got.Logs != tc.logs {
				t.Fatalf("unexpected roots: %+v", got)
			}
			if len(filepath.Join(got.Runtime, "flowsd.sock")) >= 104 {
				t.Fatalf("socket path is too long: %s", got.Runtime)
			}
			env := got.Environment()
			child, err := resolve(env["HOME"], env["XDG_CONFIG_HOME"], env["FLOWS_HOME"], 501)
			if err != nil || child != got {
				t.Fatalf("child paths = %+v, error = %v", child, err)
			}
		})
	}
}

func TestRuntimeIdentity(t *testing.T) {
	first, _ := resolve("/home/one", "", "", 501)
	otherHome, _ := resolve("/home/two", "", "", 501)
	otherUID, _ := resolve("/home/one", "", "", 502)
	isolated, _ := resolve("/home/one", "", "/home/one", 501)
	seen := make(map[string]bool)
	for _, home := range []Home{first, otherHome, otherUID, isolated} {
		if seen[home.Runtime] {
			t.Fatalf("runtime path collision: %s", home.Runtime)
		}
		seen[home.Runtime] = true
	}
}

func TestInit(t *testing.T) {
	home, err := resolve("/unused", "", filepath.Join(t.TempDir(), "isolated home"), os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	home.Runtime = filepath.Join(t.TempDir(), "runtime")
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	const settings = "profile: example\n"
	if err := os.WriteFile(home.Settings(), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home.Config, home.State, home.Cache, home.Logs, home.Runtime, home.Workflows(), home.Tools(), home.Spec(), home.Artifacts(), home.Settings()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0700)
		if path == home.Settings() {
			want = 0600
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	if data, err := os.ReadFile(home.Settings()); err != nil || string(data) != settings {
		t.Fatalf("settings = %q, error = %v", data, err)
	}
	if err := os.Chmod(home.Runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err == nil {
		t.Fatal("accepted a runtime directory accessible to other users")
	}
	home.Runtime = filepath.Join(t.TempDir(), "symlink")
	if err := os.Symlink(t.TempDir(), home.Runtime); err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err == nil {
		t.Fatal("accepted a runtime directory symlink")
	}
}
