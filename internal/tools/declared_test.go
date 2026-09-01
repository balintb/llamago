package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manifestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const echoManifest = `{
  "name": "greet",
  "description": "Greet someone by name",
  "parameters": {"type":"object","properties":{"who":{"type":"string"}},"required":["who"]},
  "command": ["/bin/echo", "hello {{who}}"],
  "safe": true
}`

func TestDeclaredToolRuns(t *testing.T) {
	dir := manifestDir(t, map[string]string{"greet.json": echoManifest})
	manifests, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if len(manifests) != 1 {
		t.Fatalf("%d manifests loaded, want 1", len(manifests))
	}
	m := manifests[0]
	if m.Name() != "greet" || !m.Safe() {
		t.Fatalf("name = %q, safe = %v", m.Name(), m.Safe())
	}
	out, err := m.Run(context.Background(), map[string]any{"who": "Lisbon"})
	if err != nil || out != "hello Lisbon" {
		t.Fatalf("run = %q, %v", out, err)
	}
}

// The whole reason command is argv rather than a shell line: an argument can say anything and remains one argument.
func TestArgumentsCannotEscapeIntoAShell(t *testing.T) {
	dir := manifestDir(t, map[string]string{"greet.json": echoManifest})
	manifests, _ := Load(dir)

	for _, hostile := range []string{
		"; rm -rf ~",
		"$(whoami)",
		"`id`",
		"a && echo pwned",
		"| cat /etc/passwd",
	} {
		out, err := manifests[0].Run(context.Background(), map[string]any{"who": hostile})
		if err != nil {
			t.Fatalf("run failed for %q: %v", hostile, err)
		}
		if out != "hello "+hostile {
			t.Fatalf("argument %q was interpreted rather than passed through: %q", hostile, out)
		}
	}
}

// A manifest that cannot work must say so at load, not at the moment a model finally calls it.
func TestBadManifestsAreRejectedAtLoad(t *testing.T) {
	cases := map[string]string{
		"no-name.json":     `{"description":"x","command":["/bin/echo"]}`,
		"bad-name.json":    `{"name":"Bad Name","description":"x","command":["/bin/echo"]}`,
		"no-desc.json":     `{"name":"a","command":["/bin/echo"]}`,
		"no-command.json":  `{"name":"b","description":"x"}`,
		"bad-timeout.json": `{"name":"c","description":"x","command":["/bin/echo"],"timeout":"soon"}`,
		"unknown-arg.json": `{"name":"d","description":"x","command":["/bin/echo","{{nope}}"],
		                      "parameters":{"type":"object","properties":{"who":{"type":"string"}}}}`,
		"not-json.json": `{`,
	}
	dir := manifestDir(t, cases)
	manifests, errs := Load(dir)

	if len(manifests) != 0 {
		t.Fatalf("%d bad manifests loaded", len(manifests))
	}
	if len(errs) != len(cases) {
		t.Fatalf("%d errors for %d bad manifests", len(errs), len(cases))
	}
	// Each error has to name its file, or there is no way to find the problem.
	for _, err := range errs {
		if !strings.Contains(err.Error(), ".json") {
			t.Errorf("error does not name the file: %v", err)
		}
	}
}

// One bad file must not cost the good ones.
func TestOneBadManifestDoesNotStopTheRest(t *testing.T) {
	dir := manifestDir(t, map[string]string{
		"greet.json":  echoManifest,
		"broken.json": `{"name":"broken"}`,
	})
	manifests, errs := Load(dir)
	if len(manifests) != 1 || len(errs) != 1 {
		t.Fatalf("%d manifests and %d errors, want 1 and 1", len(manifests), len(errs))
	}
}

func TestStdinManifestGetsJSON(t *testing.T) {
	dir := manifestDir(t, map[string]string{"cat.json": `{
	  "name": "echo_args",
	  "description": "Echo the arguments back",
	  "parameters": {"type":"object","properties":{"who":{"type":"string"}}},
	  "command": ["/bin/cat"],
	  "stdin": true
	}`})
	manifests, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	out, err := manifests[0].Run(context.Background(), map[string]any{"who": "Lisbon"})
	if err != nil || !strings.Contains(out, `"who":"Lisbon"`) {
		t.Fatalf("stdin tool got %q, %v", out, err)
	}
}

// A tool that hangs must not hang the conversation.
func TestTimeoutIsEnforced(t *testing.T) {
	dir := manifestDir(t, map[string]string{"slow.json": `{
	  "name": "slow",
	  "description": "Sleeps",
	  "command": ["/bin/sleep", "5"],
	  "timeout": "100ms"
	}`})
	manifests, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	_, err := manifests[0].Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("error = %v, want a timeout", err)
	}
}

// A failing program reports what it said on stderr, since that is what tells the model what to do differently.
func TestFailureReportsStderr(t *testing.T) {
	dir := manifestDir(t, map[string]string{"fail.json": `{
	  "name": "fail",
	  "description": "Always fails",
	  "command": ["/bin/sh", "-c", "echo nope 1>&2; exit 3"]
	}`})
	manifests, _ := Load(dir)
	_, err := manifests[0].Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error = %v, want the program's own message", err)
	}
}

// A declared tool may deliberately replace a built-in.
func TestDeclaredToolShadowsABuiltin(t *testing.T) {
	dir := manifestDir(t, map[string]string{"read.json": `{
	  "name": "read_file",
	  "description": "My own reader",
	  "parameters": {"type":"object","properties":{"path":{"type":"string"}}},
	  "command": ["/bin/echo", "mine"]
	}`})
	r := NewRegistry()
	Builtins(r, t.TempDir())
	builtins := r.Len()
	manifests, _ := Load(dir)
	for _, m := range manifests {
		r.Add(m)
	}
	tool, _ := r.Get("read_file")
	if tool.Definition().Function.Description != "My own reader" {
		t.Fatal("the declared tool did not replace the built-in")
	}
	if r.Len() != builtins {
		t.Fatalf("%d tools, want the built-in replaced rather than added alongside", r.Len())
	}
}

// grep exits 1 when it finds nothing, which is an answer rather than a failure. Without ok_exit the model is told the search broke.
func TestOkExitCodesAreNotFailures(t *testing.T) {
	dir := manifestDir(t, map[string]string{"search.json": `{
	  "name": "search",
	  "description": "Search",
	  "parameters": {"type":"object","properties":{"pattern":{"type":"string"}}},
	  "command": ["/bin/sh", "-c", "exit 1"],
	  "ok_exit": [0, 1]
	}`})
	manifests, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	out, err := manifests[0].Run(context.Background(), map[string]any{"pattern": "x"})
	if err != nil {
		t.Fatalf("exit 1 reported as a failure despite ok_exit: %v", err)
	}
	if out != "(no output)" {
		t.Fatalf("out = %q, want the model told there was nothing", out)
	}
}

// An exit code not listed is still a failure.
func TestUnlistedExitCodeStillFails(t *testing.T) {
	dir := manifestDir(t, map[string]string{"boom.json": `{
	  "name": "boom",
	  "description": "Fails",
	  "command": ["/bin/sh", "-c", "echo bad 1>&2; exit 2"],
	  "ok_exit": [0, 1]
	}`})
	manifests, _ := Load(dir)
	if _, err := manifests[0].Run(context.Background(), nil); err == nil {
		t.Fatal("exit 2 was accepted")
	}
}
