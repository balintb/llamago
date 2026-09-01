package tools

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/balintb/llamago/internal/ollama"
)

// Limits on what a tool may hand back. A model has a context window, and a tool that fills it with one directory listing has cost more than it gave.
const (
	maxFileBytes = 64 << 10
	maxListed    = 200
	maxFetchByte = 64 << 10
	fetchTimeout = 20 * time.Second
)

// Builtins registers the tools that ship with llamago. root confines the ones that touch the filesystem.
func Builtins(r *Registry, root string) {
	r.Add(&readFile{root: root})
	r.Add(&listFiles{root: root})
	r.Add(&findFiles{root: root})
	r.Add(&fileInfo{root: root})
	r.Add(&now{})
	r.Add(&httpGet{})
}

// --- filesystem, confined to root -------------------------------------------

// resolve turns a model-supplied path into a real one inside root, or refuses.
//
// Symlinks are resolved before the check, so a link pointing out of the tree is caught: the model asking for a path is not the one that has to be trusted, the resolved path is.
func resolve(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("paths are relative to the working directory; %q is not", path)
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, path)
	}
	full = filepath.Clean(full)

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	// The target may not exist yet; check the nearest existing ancestor so a missing file still reports as missing rather than as out of bounds.
	probe := full
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			probe = resolved
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	if probe != realRoot && !strings.HasPrefix(probe, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is outside the working directory, which is the only place I can read", path)
	}
	return full, nil
}

type readFile struct{ root string }

func (t *readFile) Name() string { return "read_file" }
func (t *readFile) Safe() bool   { return true }
func (t *readFile) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "Read a text file from the working directory. " +
			"Use when you need the actual contents of a file rather than a guess.",
		Parameters: schema([]string{"path"}, map[string]any{
			"path": prop("string", "Path relative to the working directory, e.g. internal/ui/app.go"),
		}),
	}}
}

func (t *readFile) Run(_ context.Context, args map[string]any) (string, error) {
	path, err := resolve(t.root, argString(args, "path"))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_files", argString(args, "path"))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(data) > maxFileBytes
	if truncated {
		data = data[:maxFileBytes]
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not a text file", argString(args, "path"))
	}
	out := string(data)
	if truncated {
		out += fmt.Sprintf("\n\n(truncated at %d KB of %d KB)", maxFileBytes>>10, info.Size()>>10)
	}
	return out, nil
}

type listFiles struct{ root string }

func (t *listFiles) Name() string { return "list_files" }
func (t *listFiles) Safe() bool   { return true }
func (t *listFiles) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "List the files and directories in one directory of the working directory. " +
			"Use to find out what exists before reading anything.",
		Parameters: schema(nil, map[string]any{
			"path": prop("string", "Directory relative to the working directory. Defaults to the working directory itself."),
		}),
	}}
}

func (t *listFiles) Run(_ context.Context, args map[string]any) (string, error) {
	path, err := resolve(t.root, argString(args, "path"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	var out []string
	for i, e := range entries {
		if i >= maxListed {
			out = append(out, fmt.Sprintf("… and %d more", len(entries)-i))
			break
		}
		if e.IsDir() {
			out = append(out, e.Name()+"/")
			continue
		}
		size := ""
		if info, err := e.Info(); err == nil {
			size = fmt.Sprintf("  %d bytes", info.Size())
		}
		out = append(out, e.Name()+size)
	}
	if len(out) == 0 {
		return "(empty directory)", nil
	}
	return strings.Join(out, "\n"), nil
}

type findFiles struct{ root string }

func (t *findFiles) Name() string { return "find_files" }
func (t *findFiles) Safe() bool   { return true }
func (t *findFiles) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "Find files whose name matches a pattern, anywhere under the working directory. " +
			"Use when you know roughly what a file is called but not where it is.",
		Parameters: schema([]string{"pattern"}, map[string]any{
			"pattern": prop("string", "Shell-style name pattern, e.g. *_test.go or config.*"),
			"path":    prop("string", "Directory to search under. Defaults to the working directory."),
		}),
	}}
}

func (t *findFiles) Run(_ context.Context, args map[string]any) (string, error) {
	base, err := resolve(t.root, argString(args, "path"))
	if err != nil {
		return "", err
	}
	pattern := strings.TrimSpace(argString(args, "pattern"))
	if pattern == "" {
		return "", fmt.Errorf("no pattern given")
	}
	if _, err := filepath.Match(pattern, "probe"); err != nil {
		return "", fmt.Errorf("%q is not a valid pattern: %w", pattern, err)
	}

	var found []string
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner is not worth failing the search
		}
		if d.IsDir() {
			// Skip the places that would swamp the result with noise.
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist":
				return fs.SkipDir
			}
			return nil
		}
		if ok, _ := filepath.Match(pattern, d.Name()); ok {
			rel, relErr := filepath.Rel(t.root, p)
			if relErr != nil {
				rel = p
			}
			found = append(found, rel)
			if len(found) >= maxListed {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return fmt.Sprintf("nothing matching %q", pattern), nil
	}
	sort.Strings(found)
	return strings.Join(found, "\n"), nil
}

// --- everything else --------------------------------------------------------

type now struct{}

func (t *now) Name() string { return "now" }
func (t *now) Safe() bool   { return true }
func (t *now) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "The current date, time and timezone. " +
			"Use for anything depending on today's date; you cannot know it otherwise.",
		Parameters: schema(nil, map[string]any{}),
	}}
}

func (t *now) Run(_ context.Context, _ map[string]any) (string, error) {
	return time.Now().Format("Monday, 2 January 2006, 15:04 MST"), nil
}

type httpGet struct{}

func (t *httpGet) Name() string { return "http_get" }

// Safe is false: this one leaves the machine, and where it goes is decided by whatever the model read a moment ago.
func (t *httpGet) Safe() bool { return false }

func (t *httpGet) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "Fetch a URL and return the response as text. " +
			"Use for looking something up that you would otherwise be guessing at.",
		Parameters: schema([]string{"url"}, map[string]any{
			"url": prop("string", "Full URL, including https://"),
		}),
	}}
}

func (t *httpGet) Run(ctx context.Context, args map[string]any) (string, error) {
	url := strings.TrimSpace(argString(args, "url"))
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("%q is not an http or https URL", url)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "llamago")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchByte+1))
	if err != nil {
		return "", err
	}
	truncated := len(data) > maxFetchByte
	if truncated {
		data = data[:maxFetchByte]
	}
	out := fmt.Sprintf("HTTP %d\n\n%s", resp.StatusCode, data)
	if truncated {
		out += fmt.Sprintf("\n\n(truncated at %d KB)", maxFetchByte>>10)
	}
	return out, nil
}

// maxLineCount is how much of a file will be read to count its lines. Past this the count is not worth the read, and the size already answers the question that was really being asked.
const maxLineCount = 8 << 20

type fileInfo struct{ root string }

func (t *fileInfo) Name() string { return "file_info" }
func (t *fileInfo) Safe() bool   { return true }
func (t *fileInfo) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name: t.Name(),
		Description: "Report what a file or directory is: size, kind, when it changed, permissions, " +
			"and how many lines or entries it holds. Use before reading something, to find out " +
			"whether it is text, how large it is, or whether it exists at all.",
		Parameters: schema([]string{"path"}, map[string]any{
			"path": prop("string", "Path relative to the working directory, e.g. internal/ui/app.go"),
		}),
	}}
}

func (t *fileInfo) Run(_ context.Context, args map[string]any) (string, error) {
	given := argString(args, "path")
	path, err := resolve(t.root, given)
	if err != nil {
		return "", err
	}
	// Lstat first: a symlink's own identity is part of the answer, and Stat would silently report whatever it points at instead.
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}

	rel, relErr := filepath.Rel(t.root, path)
	if relErr != nil {
		rel = path
	}
	out := []string{
		"path: " + rel,
		"kind: " + describeKind(info),
		"modified: " + info.ModTime().Format("2006-01-02 15:04:05") +
			"  (" + ollama.HumanSince(info.ModTime()) + ")",
		"permissions: " + info.Mode().Perm().String(),
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err == nil {
			out = append(out, "points to: "+target)
		}
		// Whether the target is reachable matters more than the link itself.
		if _, err := os.Stat(path); err != nil {
			out = append(out, "target: unreachable")
		}
	case info.IsDir():
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		var dirs, files int
		for _, e := range entries {
			if e.IsDir() {
				dirs++
				continue
			}
			files++
		}
		out = append(out, fmt.Sprintf("holds: %s, %s", plural(files, "file"), plural(dirs, "directory")))
	default:
		out = append(out, "size: "+ollama.HumanBytes(info.Size())+
			fmt.Sprintf("  (%d bytes)", info.Size()))
		out = append(out, describeContents(path, info.Size())...)
	}
	return strings.Join(out, "\n"), nil
}

// describeKind names what something is in the terms a model asks about.
func describeKind(info os.FileInfo) string {
	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		return "symbolic link"
	case mode.IsDir():
		return "directory"
	case mode&os.ModeNamedPipe != 0:
		return "named pipe"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode.Perm()&0o111 != 0:
		return "executable file"
	default:
		return "file"
	}
}

// describeContents reports whether a file is text and how many lines it holds, which is what decides whether reading it is worth doing.
func describeContents(path string, size int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	head := make([]byte, min64(size, 8<<10))
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if n == 0 {
		return []string{"contents: empty"}
	}
	if !looksTextual(head) {
		return []string{"contents: binary"}
	}
	if size > maxLineCount {
		return []string{"contents: text"}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return []string{"contents: text"}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return []string{"contents: text"}
	}
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		lines++
	}
	return []string{"contents: text, " + plural(lines, "line")}
}

// looksTextual is the same test the text attachment uses: a NUL byte in the first few KB is the usual tell, and invalid UTF-8 backs it up.
func looksTextual(head []byte) bool {
	if slices.Contains(head, 0) {
		return false
	}
	return utf8.Valid(head)
}

// plural renders a count with its unit, so "1 line" rather than "1 lines".
func plural(n int, unit string) string {
	switch {
	case n == 1:
		return "1 " + unit
	case strings.HasSuffix(unit, "y"):
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(unit, "y"))
	default:
		return fmt.Sprintf("%d %ss", n, unit)
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
