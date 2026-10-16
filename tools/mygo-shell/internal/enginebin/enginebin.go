// Package enginebin carries the AirPlay engine inside the shell's own binary.
//
// The engine is a separate process on purpose: a Go runtime cannot hold the
// engine's 7.98 ms send deadline while the UI shares it, so the audio path
// lives outside. Embedding the executable keeps that split without asking the
// user to manage two files: the shell writes the engine out once, and reuses it
// after that.
package enginebin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// payload is the directory the build drops the engine into. Embedding the
// directory rather than the file lets a checkout with no engine still build.
//
//go:embed all:payload
var payload embed.FS

// payloadPath is where the engine sits inside the embedded tree.
func payloadPath() string { return "payload/" + Name() }

// Name is the file name the engine is written as.
func Name() string {
	if runtime.GOOS == "windows" {
		return "airflash-engine.exe"
	}
	return "airflash-engine"
}

// read returns the embedded engine, or nil when none was bundled.
func read() []byte {
	data, err := fs.ReadFile(payload, payloadPath())
	if err != nil {
		return nil
	}
	return data
}

// Bundled reports whether this build carries an engine.
func Bundled() bool { return len(read()) > 0 }

// Size is how many bytes of engine this build carries.
func Size() int { return len(read()) }

// Ensure writes the embedded engine to a cache directory and returns its path.
//
// The file is named after a digest of its own contents, so an upgraded shell
// never reuses a stale engine, and two versions can coexist without a lock. A
// file already there with the right size is left alone: the name is a digest of
// the contents, so the same size means the same bytes.
func Ensure(cacheDir string) (string, error) {
	data := read()
	if len(data) == 0 {
		return "", errors.New("no engine is bundled in this build")
	}

	digest := sha256.Sum256(data)
	name := fmt.Sprintf("%s-%s", Name(), hex.EncodeToString(digest[:8]))
	dir := filepath.Join(cacheDir, "engine")
	target := filepath.Join(dir, name)

	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(data)) {
		return target, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create engine cache: %w", err)
	}

	// Write beside the target and rename, so a crash cannot leave a partial
	// file that the size check would later accept.
	tmp, err := os.CreateTemp(dir, name+".part-*")
	if err != nil {
		return "", fmt.Errorf("create engine file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // harmless after the rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write engine: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close engine: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", fmt.Errorf("mark engine executable: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", fmt.Errorf("install engine: %w", err)
	}
	return target, nil
}

// CacheDir is where an installed shell keeps its unpacked engine.
func CacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "AirFlash")
	}
	return os.TempDir()
}
