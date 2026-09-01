package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/balintb/llamago/internal/ollama"
)

// spec is the JSON shape of a manifest. It is separate from Manifest because the interface needs Name() and Safe() as methods, which a field of either name would collide with.
type spec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// Command is argv, not a shell line. "{{arg}}" anywhere in an element is replaced by that argument.
	Command []string `json:"command"`
	// Stdin sends the arguments as a JSON object on standard input instead of substituting them into Command.
	Stdin bool `json:"stdin,omitempty"`
	// Safe marks a tool that only reads and may run without asking.
	Safe    bool   `json:"safe,omitempty"`
	Timeout string `json:"timeout,omitempty"`
	// OkExit lists exit codes that are not failures. grep exits 1 when it finds nothing, which is an answer rather than an error; without this the model would be told the search failed.
	OkExit []int `json:"ok_exit,omitempty"`
	// Dir is where the program runs; the working directory by default.
	Dir string `json:"dir,omitempty"`
}

// Manifest is a tool declared as a JSON file rather than written in Go. It is the whole extension mechanism: a name, what the model should know about it, and a program to run.
type Manifest struct {
	spec    spec
	path    string
	timeout time.Duration
}

// Path is the file the manifest came from, for an error that has to say where.
func (m *Manifest) Path() string { return m.path }

const defaultToolTimeout = 30 * time.Second

// placeholder matches {{name}} in a command element.
var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// validName is what a model can reliably ask for and what a manifest may claim.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Load reads every manifest in dir. A manifest that does not make sense is reported and skipped rather than taking the rest down with it: one bad file should not cost you the tools that are fine.
func Load(dir string) ([]*Manifest, []error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}

	var out []*Manifest
	var errs []error
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		m, err := loadManifest(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		out = append(out, m)
	}
	return out, errs
}

func loadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sp spec
	if err := json.Unmarshal(b, &sp); err != nil {
		return nil, err
	}
	m := &Manifest{spec: sp, path: path}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manifest) validate() error {
	if !validName.MatchString(m.spec.Name) {
		return fmt.Errorf("name %q must be lower-case letters, digits and underscores", m.spec.Name)
	}
	if strings.TrimSpace(m.spec.Description) == "" {
		return fmt.Errorf("description is empty; it is how the model decides whether to call the tool")
	}
	if len(m.spec.Command) == 0 {
		return fmt.Errorf("command is empty")
	}
	if m.spec.Parameters == nil {
		m.spec.Parameters = schema(nil, map[string]any{})
	}
	// Every placeholder has to name a declared parameter, or the tool will fail at the worst moment rather than at load.
	declared := m.declaredParams()
	for _, arg := range m.spec.Command {
		for _, match := range placeholder.FindAllStringSubmatch(arg, -1) {
			if !declared[match[1]] {
				return fmt.Errorf("command uses {{%s}}, which is not a declared parameter", match[1])
			}
		}
	}
	m.timeout = defaultToolTimeout
	if m.spec.Timeout != "" {
		d, err := time.ParseDuration(m.spec.Timeout)
		if err != nil {
			return fmt.Errorf("timeout %q: %w", m.spec.Timeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("timeout %q is not positive", m.spec.Timeout)
		}
		m.timeout = d
	}
	return nil
}

// declaredParams is the set of parameter names in the schema.
func (m *Manifest) declaredParams() map[string]bool {
	out := map[string]bool{}
	props, _ := m.spec.Parameters["properties"].(map[string]any)
	for name := range props {
		out[name] = true
	}
	return out
}

func (m *Manifest) Name() string { return m.spec.Name }
func (m *Manifest) Safe() bool   { return m.spec.Safe }

func (m *Manifest) Definition() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.ToolFunction{
		Name:        m.spec.Name,
		Description: m.spec.Description,
		Parameters:  m.spec.Parameters,
	}}
}

// exitIsFine reports whether the program's exit code is one the manifest said to accept.
func (m *Manifest) exitIsFine(cmd *exec.Cmd) bool {
	if cmd.ProcessState == nil {
		return false
	}
	for _, code := range m.spec.OkExit {
		if cmd.ProcessState.ExitCode() == code {
			return true
		}
	}
	return false
}

// Run executes the declared program.
//
// There is no shell: Command is argv, and an argument is substituted as one whole element. A model asking for a city called `; rm -rf ~` produces a program looking for a strangely named city, not a deleted home directory.
func (m *Manifest) Run(ctx context.Context, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	argv := make([]string, len(m.spec.Command))
	for i, part := range m.spec.Command {
		argv[i] = placeholder.ReplaceAllStringFunc(part, func(match string) string {
			name := placeholder.FindStringSubmatch(match)[1]
			return argString(args, name)
		})
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = m.spec.Dir
	if m.spec.Stdin {
		payload, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		cmd.Stdin = bytes.NewReader(payload)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s took longer than %s", m.spec.Name, m.timeout)
	}
	if err != nil && !m.exitIsFine(cmd) {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	out := strings.TrimRight(stdout.String(), "\n")
	if out == "" {
		// A tool that succeeded and printed nothing has said something: the model needs to hear "nothing", not an empty string it may ignore.
		out = "(no output)"
	}
	return out, nil
}
