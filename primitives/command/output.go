package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Mode is how a result is rendered (docs/org/cli-guide.md, Output modes).
type Mode int

const (
	// Human is the mode for a person at a terminal. The human form is a
	// rendering: its labels, its widths and its order are made for whoever is
	// reading, and they are improved for that reader without notice. A reader
	// that parses it is a defect, whatever it parses today.
	Human Mode = iota
	// JSON is the mode for anything reading the result. It is the stable
	// interface, and it evolves additively.
	JSON
)

// String names the mode as an error reports it.
func (m Mode) String() string {
	if m == JSON {
		return "json"
	}
	return "human"
}

// selectMode applies the guide's rule: -json or -human forces it, and otherwise
// stdout being a character device decides. Passing both is a contradiction the
// library checks itself.
//
// The mode is decided by stdout only — never stderr, never an environment
// variable — and it is decided for every command a tool has, -help and -version
// included. A rule with one exception has to be known, where a rule without one
// can be assumed.
func selectMode(wantJSON, wantHuman, outIsTerminal bool) (Mode, error) {
	switch {
	case wantJSON && wantHuman:
		return Human, fmt.Errorf("-json and -human ask for two modes: pass one, or neither and let stdout decide")
	case wantJSON:
		return JSON, nil
	case wantHuman:
		return Human, nil
	case outIsTerminal:
		return Human, nil
	}
	return JSON, nil
}

// writeResult writes the result once, whole, in the mode the invocation
// selected. An action never reaches stdout any other way.
//
// Both modes are rendered before anything is written, so a failure while
// rendering leaves stdout untouched rather than half a result on the stream a
// caller is parsing.
func writeResult(res Result, mode Mode, out io.Writer) error {
	var buf bytes.Buffer
	if mode == JSON {
		body, err := json.Marshal(res)
		if err != nil {
			return fmt.Errorf("encoding the result: %w", err)
		}
		buf.Write(body)
		buf.WriteByte('\n')
	} else if err := res.Human(&buf); err != nil {
		return fmt.Errorf("rendering the result: %w", err)
	}
	_, err := out.Write(buf.Bytes())
	return err
}
