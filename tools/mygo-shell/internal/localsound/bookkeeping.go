package localsound

// The bookkeeping the silence leans on, and nothing else.
//
// This lives apart from the Windows verbs so it can be tested where the verbs
// cannot run: whether an endpoint is remembered, and what it is restored to, is
// the one part of the feature with a rule worth asserting. The verbs are COM
// calls that only exist on Windows.

import (
	"errors"
	"strings"
	"sync"
)

// remembered is one endpoint this silenced.
type remembered struct {
	// wasMuted is the state the endpoint was in, so Restore can put it back
	// rather than turn it on: the user's speakers were not necessarily on.
	wasMuted bool
	// read says whether the state was ever read. An endpoint whose state could
	// not be read is not remembered, because restoring it would mean this app
	// changing a setting the user made.
	read bool
}

var (
	mu   sync.Mutex
	kept = map[string]remembered{}
)

// remember records one endpoint's state, unless it is already remembered.
//
// Only the first one counts: a second silence of the same endpoint would
// otherwise record the muted state this caused, and restoring that would leave
// the endpoint muted.
func remember(id string, was remembered) {
	mu.Lock()
	defer mu.Unlock()
	if _, already := kept[id]; already {
		return
	}
	kept[id] = was
}

// takeForRestore is what has been remembered, cleared for the next session.
func takeForRestore() map[string]remembered {
	mu.Lock()
	defer mu.Unlock()
	pending := kept
	kept = map[string]remembered{}
	return pending
}

// putBack keeps endpoints that could not be restored, without replacing a
// state a newer silence has already recorded.
func putBack(pending map[string]remembered) {
	mu.Lock()
	defer mu.Unlock()
	for id, was := range pending {
		if _, already := kept[id]; !already {
			kept[id] = was
		}
	}
}

// restoreRemembered runs one restore attempt with the platform's apartment
// opener. A failed attempt keeps everything it took, so a later attempt can
// still give the user their speakers back.
func restoreRemembered(
	join func(func() error) error,
	restoreOne func(string, remembered) error,
) error {
	pending := takeForRestore()
	var failures []string
	failed := map[string]remembered{}

	joinErr := join(func() error {
		for id, was := range pending {
			if !was.read {
				continue
			}
			if err := restoreOne(id, was); err != nil {
				failed[id] = was
				failures = append(failures, id+": "+err.Error())
			}
		}
		return nil
	})
	if joinErr != nil {
		putBack(pending)
		return joinErr
	}
	putBack(failed)
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// guid is a Guid in the layout a COM signature takes.
type guid struct {
	D1 uint32
	D2 uint16
	D3 uint16
	D4 [8]byte
}

// guidx parses the string form of a Guid.
//
// The two final groups are read as bytes in the order written: "8E3D" is 0x8E
// then 0x3D. A Guid is not an integer, so the leading group is one and the
// trailing one is not - reading both as integers is what produces a value that
// matches nothing at all.
func guidx(text string) guid {
	var g guid
	fifth, fourth, third, second, first := splitGuid(text)
	if first == "" {
		return g
	}
	// The first three groups are fixed-width integers in their own right.
	g.D1 = uint32(readHex(first))
	g.D2 = uint16(readHex(second))
	g.D3 = uint16(readHex(third))
	// The last two are bytes, in the order the string writes them. Which is the
	// order they are stored in as well: 8E3D is the two bytes 0x8E 0x3D, and
	// C4579291692E is the six bytes C4 57 92 91 69 2E, appended after them.
	for i := 0; i < len(fourth); i += 2 {
		g.D4[i/2] = byte(readHex(fourth[i : i+2]))
	}
	base := len(fourth) / 2
	for i := 0; i < len(fifth) && base+i/2 < len(g.D4); i += 2 {
		g.D4[base+i/2] = byte(readHex(fifth[i : i+2]))
	}
	return g
}

// splitGuid cuts a Guid string into its five groups, or nothing.
func splitGuid(text string) (fifth, fourth, third, second, first string) {
	parts := strings.Split(text, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 ||
		len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return "", "", "", "", ""
	}
	return parts[4], parts[3], parts[2], parts[1], parts[0]
}

// readHex reads hexadecimal as a number, or zero where the text is not
// hexadecimal. An unparsable Guid has to be a value no COM class answers to,
// which a zeroed Guid - itself a real class - would not be.
func readHex(text string) uint64 {
	var out uint64
	for _, r := range text {
		v := hexDigit(r)
		if v < 0 {
			return 0
		}
		out = out<<4 | uint64(v)
	}
	return out
}

func hexDigit(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}
