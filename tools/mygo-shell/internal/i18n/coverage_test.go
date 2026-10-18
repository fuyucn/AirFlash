package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every string the interface asks the translator for must have a Chinese entry.
//
// The other coverage test is a hand-kept list, so it only checks the strings
// somebody remembered to add to it. This reads the source instead: every call to
// the translator's T with a literal key is collected, and each one is looked up.
// A key that falls back to English is the bug this exists to catch - it is how
// "incomplete", "stopped" and "the receiver" reached a Chinese screen.
func TestEveryTranslatedKeyHasAChineseEntry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := translatedKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) < 80 {
		t.Fatalf("found only %d keys, which means the scan is not reading the source", len(keys))
	}
	// These are not prose: a product name, a state word already drawn in
	// upper case as a label, and the em dash that stands for "no reading yet".
	// They are the same in both languages, so an entry for them would say
	// nothing.
	same := map[string]bool{"AIRFLASH": true, "LIGHTWEIGHT": true, "—": true}

	zh := For(Chinese)
	for _, key := range keys {
		if same[key] {
			continue
		}
		if zh.T(key) == key {
			t.Errorf("%q is asked for but has no Chinese entry", key)
		}
	}
}

// translatedKeys collects the literal keys the ui package passes to a
// translator, joining adjacent string literals so a wrapped sentence is one key.
func translatedKeys(root string) ([]string, error) {
	call := regexp.MustCompile(`(?s)(?:s\.t|p\.t|words\.T)\(\s*((?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+)\)`)
	part := regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

	seen := map[string]bool{}
	var out []string
	err := filepath.Walk(filepath.Join(root, "ui"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			var b strings.Builder
			for _, p := range part.FindAllStringSubmatch(m[1], -1) {
				b.WriteString(p[1])
			}
			key := b.String()
			if key != "" && !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
		return nil
	})
	return out, err
}
