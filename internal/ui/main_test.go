package ui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain redirects the config directory into a throwaway temp dir for the whole package.
//
// The live tests drive real chats, and a completed turn saves. Every path underneath - sessions, attachments, exports and config.json itself - resolves through config.Dir, so without this the suite writes its "Say hello in one short sentence." conversations straight into the developer's own ~/.config/llamago and the sidebar grows by one chat per test per run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "llamago-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ui: cannot create temp config dir:", err)
		os.Exit(1)
	}
	// config.Dir reads this on every call, so setting it here covers the package. Tests must not use t.Setenv for it: that would restore the real directory partway through the run.
	os.Setenv("XDG_CONFIG_HOME", dir)

	code := m.Run()

	// os.Exit skips deferred calls, so clean up first.
	os.RemoveAll(dir)
	os.Exit(code)
}
