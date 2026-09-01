package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInlinedFileIsFencedAndNamed(t *testing.T) {
	a := chatWithPrompts(t)
	path := writeTemp(t, "main.go", "package main\n\nfunc main() {}\n")
	a.attachText(path)

	got := a.input.Value()
	if !strings.HasPrefix(got, "main.go:\n```go\n") {
		t.Fatalf("composer = %q, want the file named and fenced as go", got)
	}
	if !strings.Contains(got, "func main() {}") {
		t.Fatal("the contents are missing")
	}
}

// Attaching under a question already typed has to keep the question.
func TestInliningAppendsToWhatIsTyped(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "what does this do?")
	a.attachText(writeTemp(t, "x.txt", "hello"))

	got := a.input.Value()
	if !strings.HasPrefix(got, "what does this do?") {
		t.Fatalf("composer = %q, want the question kept", got)
	}
	if !strings.Contains(got, "x.txt:") {
		t.Fatal("the file was not appended")
	}
}

// A binary that slipped past the extension list is refused rather than pasted as mojibake.
func TestBinaryFilesAreRefused(t *testing.T) {
	a := chatWithPrompts(t)
	path := filepath.Join(t.TempDir(), "blob.txt")
	if err := os.WriteFile(path, []byte{0x7f, 0x45, 0x4c, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	a.attachText(path)

	if a.input.Value() != "" {
		t.Fatalf("composer = %q, want a binary refused", a.input.Value())
	}
	if !a.toastErr {
		t.Errorf("toast = %q, want it to report a non-text file", a.toast)
	}
}

func TestOversizedFilesAreRefused(t *testing.T) {
	a := chatWithPrompts(t)
	path := writeTemp(t, "big.txt", strings.Repeat("x", maxTextAttachment+1))
	a.attachText(path)

	if a.input.Value() != "" {
		t.Fatal("an oversized file was inlined")
	}
	if !a.toastErr || !strings.Contains(a.toast, "too large") {
		t.Errorf("toast = %q, want a size complaint", a.toast)
	}
}

func TestFenceLanguage(t *testing.T) {
	for path, want := range map[string]string{
		"a.go": "go", "go.mod": "go", "x.yml": "yaml", "run.sh": "bash",
		"README.md": "md", "Makefile": "",
	} {
		if got := fenceLang(path); got != want {
			t.Errorf("fenceLang(%q) = %q, want %q", path, got, want)
		}
	}
}
