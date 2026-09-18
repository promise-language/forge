package tooling

// The link-time stamp (docs/project-tools.md, Make step 8 and Staleness).
//
// Each binary gets one stamp carrying the root it was built in, the hash of its
// source set, and the set itself. One value rather than three because the
// linker's -X takes a flag per variable and a root containing a space would
// have to survive three of them; encoded because a root containing a space,
// a quote or an equals sign must reach the binary as it was written.

import (
	"encoding/base64"
	"encoding/json"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/primitives/command"
)

// Stamp is what `make` writes into every binary it compiles.
type Stamp struct {
	// Root is the absolute repository root the binary was built in. The
	// staleness check is anchored to it, so copying a binary into another
	// repository does not trick it into re-hashing the wrong tree.
	Root string `json:"root"`
	// Hash is the digest of Dirs at the moment the binary was compiled.
	Hash string `json:"hash"`
	// Dirs is the source set the hash was taken over, so a binary recomputes
	// the same set the builder hashed rather than guessing at it.
	Dirs []string `json:"dirs"`
}

// Encode renders a stamp for the linker: URL-safe base64 of the JSON, which has
// no space, no quote and no equals sign inside it, so `-X main.stamp=<value>`
// survives the linker's own flag parsing whatever the root is called.
func (s Stamp) Encode() string {
	body, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(body)
}

// DecodeStamp reads what the linker wrote. An empty or unreadable stamp is an
// unstamped binary: built by `go build` or `go install`, not by the project's
// builder, which is one of the conditions a tool declines to run on.
func DecodeStamp(encoded string) (Stamp, bool) {
	if encoded == "" {
		return Stamp{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Stamp{}, false
	}
	var s Stamp
	if err := json.Unmarshal(body, &s); err != nil || s.Root == "" || s.Hash == "" {
		return Stamp{}, false
	}
	return s, true
}

// MayAct is what every tool but the builder answers command.Tool.Fit with: it
// reports why this binary may not act, or nil when it may.
//
// The source set comes out of the stamp, so a tool never names it and no two
// tools can name different ones — the builder hashed a set and recorded it, and
// the binary recomputes that same set.
func MayAct(tool, stamp string) func() *command.Refusal {
	return func() *command.Refusal {
		s, ok := DecodeStamp(stamp)
		if !ok {
			return &command.Refusal{
				Refusal:  command.Unstamped,
				Tool:     tool,
				Detail:   "this binary carries no stamp, so it was not built by " + primitives.MakeCmd(),
				Recovery: primitives.Recovery(),
			}
		}
		return primitives.StaleRefusal(tool, s.Root, s.Hash, s.Dirs...)
	}
}

// StampedRoot is the repository a binary was built in, or "" when it carries no
// stamp. A tool that has passed Fit has a root; one that has not never reaches
// the point of needing it.
func StampedRoot(stamp string) string {
	s, _ := DecodeStamp(stamp)
	return s.Root
}
