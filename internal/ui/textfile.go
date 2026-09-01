package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// maxTextAttachment caps what will be inlined. Past this the file is not so much context as a way to fill the window with one prompt.
const maxTextAttachment = 128 << 10

// textFileExtensions are what the browser offers. The list is a hint rather than a rule - anything that turns out to be text is accepted - but it keeps the listing readable in a source directory.
var textFileExtensions = []string{
	".go", ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".sh", ".bash",
	".zsh", ".py", ".rs", ".ts", ".tsx", ".js", ".jsx", ".c", ".h", ".cpp",
	".java", ".rb", ".php", ".sql", ".html", ".css", ".xml", ".ini", ".conf",
	".env", ".mod", ".sum", ".gitignore", ".dockerfile", ".make", ".mk",
}

// openTextPicker browses for a file to inline into the composer.
func (a *App) openTextPicker() tea.Cmd {
	a.pickerMode = pickText
	a.picker.AllowedTypes = textFileExtensions
	a.picker.DirAllowed = false
	a.picker.FileAllowed = true
	return a.openPicker()
}

// attachText inlines a file into the composer as a fenced block. The prompt is left in the composer rather than sent, so a question can be typed around it.
func (a *App) attachText(path string) tea.Cmd {
	info, err := os.Stat(path)
	if err != nil {
		return a.errToast(err)
	}
	if info.Size() > maxTextAttachment {
		return a.showToast(fmt.Sprintf("%s is %s - too large to inline",
			filepath.Base(path), humanBytes(info.Size())), true)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return a.errToast(err)
	}
	if !looksLikeText(data) {
		return a.showToast(filepath.Base(path)+" is not a text file", true)
	}

	body := strings.TrimRight(string(data), "\n")
	block := fmt.Sprintf("%s:\n```%s\n%s\n```\n", filepath.Base(path), fenceLang(path), body)

	// Append rather than replace: attaching a second file, or attaching one under a question already typed, both have to work.
	if cur := a.input.Value(); strings.TrimSpace(cur) != "" {
		block = strings.TrimRight(cur, "\n") + "\n\n" + block
	}
	a.setComposer(block)
	return a.okToast(fmt.Sprintf("%s inlined, ~%d tokens",
		filepath.Base(path), approxTokens(body)))
}

// looksLikeText rejects binaries the extension list let through. A NUL byte in the first few KB is the usual tell, and invalid UTF-8 backs it up.
func looksLikeText(data []byte) bool {
	head := data
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	if len(head) == 0 {
		return true
	}
	if slices.Contains(head, 0) {
		return false
	}
	return utf8.Valid(head)
}

// fenceLang maps an extension to a markdown fence tag, so the block is highlighted in the transcript the way the model's own code blocks are.
func fenceLang(path string) string {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case "":
		return ""
	case ".mod", ".sum":
		return "go"
	case ".yml":
		return "yaml"
	case ".sh", ".bash", ".zsh":
		return "bash"
	default:
		return ext[1:]
	}
}

// humanBytes is a short size for a message about a file being too big.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
