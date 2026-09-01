package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/balintb/llamago/internal/ollama"
)

// toolCall is what the model sends when it wants a tool run.
func toolCall(name string, args map[string]any) ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunc{Name: name, Arguments: args}}
}

func tempTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n\nfunc main() {}\n")
	write("internal/app/app.go", "package app\n")
	write("internal/app/app_test.go", "package app\n")
	return root
}

func run(t *testing.T, tool Tool, args map[string]any) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), args)
}

func TestReadFileStaysInsideTheWorkingDirectory(t *testing.T) {
	root := tempTree(t)
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &readFile{root: root}

	if out, err := run(t, tool, map[string]any{"path": "main.go"}); err != nil || !strings.Contains(out, "func main") {
		t.Fatalf("reading a file in the tree failed: %v %q", err, out)
	}

	// Everything a model might try to climb out with.
	for _, path := range []string{
		secret,
		"../" + filepath.Base(filepath.Dir(secret)) + "/id_rsa",
		"/etc/passwd",
		"~/.ssh/id_rsa",
		"internal/../../etc/passwd",
	} {
		out, err := run(t, tool, map[string]any{"path": path})
		if err == nil {
			t.Errorf("read %q, which is outside the tree: %q", path, out)
		}
		if strings.Contains(out, "PRIVATE KEY") {
			t.Fatalf("leaked the contents of %q", path)
		}
	}
}

// A symlink pointing out of the tree is the interesting case: the path looks fine and the target is not.
func TestSymlinkOutOfTheTreeIsRefused(t *testing.T) {
	root := tempTree(t)
	outside := filepath.Join(t.TempDir(), "secrets")
	if err := os.WriteFile(outside, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "innocent.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	out, err := run(t, &readFile{root: root}, map[string]any{"path": "innocent.txt"})
	if err == nil || strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("followed a symlink out of the tree: %v %q", err, out)
	}
}

func TestListAndFind(t *testing.T) {
	root := tempTree(t)

	out, err := run(t, &listFiles{root: root}, map[string]any{})
	if err != nil || !strings.Contains(out, "main.go") || !strings.Contains(out, "internal/") {
		t.Fatalf("listing the root: %v\n%s", err, out)
	}

	out, err = run(t, &findFiles{root: root}, map[string]any{"pattern": "*_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Join("internal", "app", "app_test.go")) {
		t.Fatalf("find missed the test file:\n%s", out)
	}
	if strings.Contains(out, "main.go") {
		t.Fatalf("find matched a file it should not have:\n%s", out)
	}
}

// A tool error is data for the model, not a failure of the turn.
func TestUnknownToolReportsWhatExists(t *testing.T) {
	r := NewRegistry()
	Builtins(r, t.TempDir())

	res := r.Run(context.Background(), toolCall("nonexistent", nil))
	if res.Err == nil {
		t.Fatal("calling a tool that does not exist succeeded")
	}
	msg := res.Message()
	if msg.Role != "tool" || !strings.Contains(msg.Content, "read_file") {
		t.Fatalf("the model is not told what it could have called: %+v", msg)
	}
}

func TestSafetyFlags(t *testing.T) {
	r := NewRegistry()
	Builtins(r, t.TempDir())
	for name, wantSafe := range map[string]bool{
		"read_file": true, "list_files": true, "find_files": true,
		"now": true, "http_get": false,
	} {
		tool, ok := r.Get(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if tool.Safe() != wantSafe {
			t.Errorf("%s safe = %v, want %v", name, tool.Safe(), wantSafe)
		}
	}
}

// The definitions the model sees must be stable and complete.
func TestDefinitionsAreOrderedAndDescribed(t *testing.T) {
	r := NewRegistry()
	Builtins(r, t.TempDir())
	defs := r.Definitions()
	if len(defs) != r.Len() {
		t.Fatalf("%d definitions for %d tools", len(defs), r.Len())
	}
	for i, d := range defs {
		if d.Type != "function" || d.Function.Name == "" || d.Function.Description == "" {
			t.Errorf("definition %d is incomplete: %+v", i, d)
		}
		if d.Function.Parameters == nil {
			t.Errorf("%s has no parameter schema", d.Function.Name)
		}
	}
	// Sorted, so the order the model sees is the same between runs rather than following Go's map iteration.
	for i := 1; i < len(defs); i++ {
		if defs[i-1].Function.Name >= defs[i].Function.Name {
			t.Errorf("definitions are not sorted: %q before %q",
				defs[i-1].Function.Name, defs[i].Function.Name)
		}
	}
}

func TestFileInfoReportsWhatMatters(t *testing.T) {
	root := tempTree(t)
	tool := &fileInfo{root: root}

	out, err := run(t, tool, map[string]any{"path": "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: file", "size: ", "bytes", "modified: ", "permissions: ", "contents: text, 3 lines"} {
		if !strings.Contains(out, want) {
			t.Errorf("file report is missing %q:\n%s", want, out)
		}
	}

	// A directory answers a different question.
	out, err = run(t, tool, map[string]any{"path": "internal/app"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kind: directory") || !strings.Contains(out, "2 files") {
		t.Errorf("directory report is wrong:\n%s", out)
	}
	if strings.Contains(out, "size: ") {
		t.Errorf("a directory reported a size, which means nothing useful:\n%s", out)
	}
}

// Whether a file is text is what decides if reading it is worth doing.
func TestFileInfoTellsTextFromBinary(t *testing.T) {
	root := tempTree(t)
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x00, 0x01, 0x02, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &fileInfo{root: root}

	if out, _ := run(t, tool, map[string]any{"path": "blob.bin"}); !strings.Contains(out, "contents: binary") {
		t.Errorf("a binary was not reported as one:\n%s", out)
	}
	if out, _ := run(t, tool, map[string]any{"path": "empty.txt"}); !strings.Contains(out, "contents: empty") {
		t.Errorf("an empty file was not reported as one:\n%s", out)
	}
}

// A symlink's own identity is the answer, not the file it points at.
func TestFileInfoDescribesSymlinks(t *testing.T) {
	root := tempTree(t)
	if err := os.Symlink("main.go", filepath.Join(root, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out, err := run(t, &fileInfo{root: root}, map[string]any{"path": "link.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "symbolic link") || !strings.Contains(out, "points to: main.go") {
		t.Fatalf("a symlink was reported as its target:\n%s", out)
	}
}

// It reads nothing, but it still may not report on what is out of reach.
func TestFileInfoIsConfinedToo(t *testing.T) {
	root := tempTree(t)
	if _, err := run(t, &fileInfo{root: root}, map[string]any{"path": "/etc/passwd"}); err == nil {
		t.Fatal("reported on a file outside the working directory")
	}
	if _, err := run(t, &fileInfo{root: root}, map[string]any{"path": "nope.go"}); err == nil {
		t.Fatal("a missing file was reported as existing")
	}
}

func TestPlural(t *testing.T) {
	for _, c := range []struct {
		n          int
		unit, want string
	}{
		{1, "line", "1 line"}, {2, "line", "2 lines"},
		{1, "directory", "1 directory"}, {0, "directory", "0 directories"},
	} {
		if got := plural(c.n, c.unit); got != c.want {
			t.Errorf("plural(%d, %q) = %q, want %q", c.n, c.unit, got, c.want)
		}
	}
}
