package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/paths"
)

func testHome(t *testing.T) paths.Home {
	t.Helper()
	t.Setenv("FLOWS_HOME", filepath.Join(t.TempDir(), "home"))
	home, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeToolPkg(t *testing.T, dir, id, version, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := "schema: flows/tool/v1\nid: " + id + "\nversion: " + version + "\nowner: dev\noperations:\n  - id: " + id + ".run\n    description: " + desc + "\n    execution_kind: plugin\n"
	if err := os.WriteFile(filepath.Join(dir, "tool.yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImportRepeatedEditConflictMidPublishAndRoots(t *testing.T) {
	home := testHome(t)
	src := filepath.Join(t.TempDir(), "mytool")
	writeToolPkg(t, src, "mytool", "1.0.0", "first")

	// 1. Clean home preview + import.
	prev, err := Preview(home, src)
	if err != nil || prev.Status != StatusNew || prev.Kind != KindTool {
		t.Fatalf("unexpected preview: prev=%+v err=%v", prev, err)
	}
	rec1, err := Import(home, src, ImportOptions{})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}

	// 2. Repeated identical import succeeds.
	prev2, err := Preview(home, src)
	if err != nil || prev2.Status != StatusIdentical {
		t.Fatalf("expected StatusIdentical, got %+v (err=%v)", prev2, err)
	}
	if _, err := Import(home, src, ImportOptions{}); err != nil {
		t.Fatalf("repeated identical import failed: %v", err)
	}

	// 3. Edited user copy triggers conflict unless AllowOverwrite is set.
	if err := os.WriteFile(filepath.Join(rec1.Preview.TargetDir, "NOTES.txt"), []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	prev3, err := Preview(home, src)
	if err != nil || prev3.Status != StatusConflict {
		t.Fatalf("expected StatusConflict after user edit, got %+v (err=%v)", prev3, err)
	}
	if _, err := Import(home, src, ImportOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// 4. Failure halfway through publishing preserves last known working copy.
	writeToolPkg(t, src, "mytool", "2.0.0", "second")
	_, err = Import(home, src, ImportOptions{
		AllowOverwrite: true,
		BeforePublish:  func() error { return errors.New("simulated publish failure") },
	})
	if err == nil {
		t.Fatalf("expected simulated failure")
	}
	if _, err := os.Stat(filepath.Join(rec1.Preview.TargetDir, "NOTES.txt")); err != nil {
		t.Fatalf("user copy should be preserved on mid-publish failure: %v", err)
	}

	// Locked dependency resolves directly to immutable v1 revision.
	lockedDir, err := ResolveLockedRevision(home, KindTool, "mytool", rec1.Preview.Digest)
	if err != nil || lockedDir != rec1.RevisionDir {
		t.Fatalf("ResolveLockedRevision failed: dir=%q err=%v", lockedDir, err)
	}

	// 5. External root disappearing + conflicts between roots + indexing before activation.
	extra1 := filepath.Join(t.TempDir(), "extra1")
	writeToolPkg(t, extra1, "mytool", "3.0.0", "conflict-root")
	missingExtra := filepath.Join(t.TempDir(), "gone")

	snap, err := Scan(home, []string{extra1, missingExtra})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(snap.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict between user and external root, got %+v", snap.Conflicts)
	}

	reg := catalog.NewRegistry()
	if err := PublishTools(snap, reg, nil); err != nil {
		t.Fatal(err)
	}
	op, _, err := reg.Describe("mytool.run")
	if err != nil || op.Enabled {
		t.Fatalf("expected indexed disabled tool before activation, op=%+v err=%v", op, err)
	}
}

func TestRejectEscapingSymlinkAndCaseCollision(t *testing.T) {
	home := testHome(t)
	src := filepath.Join(t.TempDir(), "bad")
	writeToolPkg(t, src, "badtool", "1.0.0", "desc")

	if err := os.Symlink("/etc/passwd", filepath.Join(src, "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(home, src); !errors.Is(err, ErrEscapingPath) {
		t.Fatalf("expected ErrEscapingPath for symlink, got %v", err)
	}
}
