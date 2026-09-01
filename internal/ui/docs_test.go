package ui

import (
	"os"
	"strings"
	"testing"
)

// docsKeys is the keymap page, which has to agree with the app's own help. The compare keys were absent from the help for a while precisely because nothing checked.
func docsKeys(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/keys.md")
	if err != nil {
		t.Skipf("keymap page not readable: %v", err)
	}
	return strings.ReplaceAll(string(b), "`", "")
}

// Every binding the help lists must appear on the page. Keys are compared by their parts, so "j / k" satisfies a page that writes them as separate cells.
func TestKeymapPageCoversTheHelp(t *testing.T) {
	page := docsKeys(t)
	for _, section := range helpSections {
		for _, k := range section.keys {
			if !mentionsKey(page, k[0]) {
				t.Errorf("docs/keys.md never mentions %q (%s), which the help lists",
					k[0], k[1])
			}
		}
	}
}

// And the sections themselves, so a whole mode cannot go undocumented.
func TestKeymapPageCoversEverySection(t *testing.T) {
	page := strings.ToLower(docsKeys(t))
	for _, section := range helpSections {
		// "Transcript / lists" is one heading in the help and two on the page.
		name := strings.ToLower(strings.Split(section.title, " / ")[0])
		if !strings.Contains(page, name) {
			t.Errorf("docs/keys.md has no section for %q", section.title)
		}
	}
}

// mentionsKey reports whether the page names a binding, allowing for the help writing "j / k" or "alt+1…4" where the page may split or spell them out.
func mentionsKey(page, key string) bool {
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '/' || r == '…' || r == ' '
	})
	// A key made entirely of separators, like "/", has no parts to split into.
	if len(parts) == 0 {
		return strings.Contains(page, key)
	}
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" && strings.Contains(page, part) {
			return true
		}
	}
	return false
}
