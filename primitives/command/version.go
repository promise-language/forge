package command

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// version is what -version answers. It is a result like any other, and the
// guide's "one line" describes its human mode: a version is the answer most
// often read by a program, since it is how one binary identifies another, so it
// selects its mode exactly as every other result does (docs/command-line.md,
// Help and version).
type version struct {
	// Project is the project the binary is built from, never folded into
	// another field.
	Project string `json:"project"`
	// Text is the version as the human line shows it, without the project
	// name. A build that recorded none omits it rather than reporting the empty
	// string.
	Text string `json:"text,omitempty"`
	// Major, Minor and Patch are present when the version is semantic, so no
	// consumer parses Text to compare one. A version that is not semantic omits
	// them rather than zeroing them: a hash has no major, and 0 would be
	// compared as a number where an absent field means unknown.
	Major *int `json:"major,omitempty"`
	Minor *int `json:"minor,omitempty"`
	Patch *int `json:"patch,omitempty"`
	// Prerelease and Build join them when the version carries those parts.
	Prerelease string `json:"prerelease,omitempty"`
	Build      string `json:"build,omitempty"`
}

// Human is "<project> <text>", and says so when the build recorded no version.
func (v version) Human(w io.Writer) error {
	if v.Text == "" {
		_, err := fmt.Fprintf(w, "%s (no version recorded)\n", v.Project)
		return err
	}
	_, err := fmt.Fprintf(w, "%s %s\n", v.Project, v.Text)
	return err
}

// versionOf reads the tool's stamp into the payload, adding the semantic parts
// when the text carries them.
func versionOf(t Tool) version {
	v := version{Project: t.Project, Text: t.Version}
	major, minor, patch, pre, build, ok := parseSemantic(t.Version)
	if !ok {
		return v
	}
	v.Major, v.Minor, v.Patch = &major, &minor, &patch
	v.Prerelease, v.Build = pre, build
	return v
}

// parseSemantic reads MAJOR.MINOR.PATCH with an optional leading v, an optional
// -prerelease and an optional +build. Anything else — a commit hash, a date, a
// word — is not semantic, and its parts are absent rather than zero.
func parseSemantic(text string) (major, minor, patch int, prerelease, build string, ok bool) {
	rest, build, _ := strings.Cut(strings.TrimPrefix(text, "v"), "+")
	core, prerelease, _ := strings.Cut(rest, "-")

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return 0, 0, 0, "", "", false
	}
	numbers := make([]int, 3)
	for i, p := range parts {
		// A leading zero is not a number semantic versions compare, so a text
		// carrying one is not semantic and keeps its parts absent.
		if !allDigits(p) || (len(p) > 1 && p[0] == '0') {
			return 0, 0, 0, "", "", false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, "", "", false
		}
		numbers[i] = n
	}
	return numbers[0], numbers[1], numbers[2], prerelease, build, true
}
