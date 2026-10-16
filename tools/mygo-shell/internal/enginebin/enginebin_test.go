package enginebin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAnUnbundledBuildSaysSoRatherThanFailing(t *testing.T) {
	// A checkout with no engine must still build and start; it reports that it
	// has nothing to run instead of crashing.
	//
	// The payload in a test build is whatever is committed, so this asserts the
	// contract rather than the contents: with no payload, Ensure must error and
	// Bundled must be false.
	if Bundled() {
		t.Skip("this build carries an engine payload")
	}
	if _, err := Ensure(t.TempDir()); err == nil {
		t.Error("Ensure should fail when no engine is bundled")
	}
}

func TestEnsureWritesTheEngineOut(t *testing.T) {
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	dir := t.TempDir()

	path, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the engine was not written: %v", err)
	}
	if info.Size() != int64(Size()) {
		t.Errorf("wrote %d bytes, want %d", info.Size(), Size())
	}
}

func TestTheWrittenEngineIsExecutable(t *testing.T) {
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	path, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode %v has no execute bit; the engine could not be started", info.Mode())
	}
}

func TestTheNameCarriesAContentDigest(t *testing.T) {
	// An upgraded shell must never reuse a stale engine, so the file name has
	// to change when the bytes do.
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	path, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, Name()+"-") {
		t.Errorf("name %q should start with %q and a digest", base, Name())
	}
	if len(base) <= len(Name())+1 {
		t.Errorf("name %q carries no digest", base)
	}
}

func TestASecondCallReusesTheFile(t *testing.T) {
	// Unpacking on every start would be wasted work.
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	dir := t.TempDir()

	first, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(first, zeroTime(), zeroTime()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("second call gave %q, want the same file %q", second, first)
	}
	after, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the file was rewritten; an existing engine should be reused")
	}
}

func TestAPartialFileIsNotMistakenForACompleteOne(t *testing.T) {
	// A crash mid-write must not leave a file the size check later accepts.
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	dir := t.TempDir()

	// A file with the right name but the wrong size is not the engine.
	target, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("short"), 0o755); err != nil {
		t.Fatal(err)
	}

	again, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(again)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(Size()) {
		t.Errorf("size %d, want the full engine %d: a truncated file was accepted", info.Size(), Size())
	}
}

func TestNoPartialFilesAreLeftBehind(t *testing.T) {
	if !Bundled() {
		t.Skip("no engine payload in this build")
	}
	dir := t.TempDir()
	if _, err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "engine"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

func zeroTime() time.Time { return time.Unix(0, 0) }
