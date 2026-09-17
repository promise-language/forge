package command

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// units is the duration grammar, and it is the only one
// (docs/org/cli-guide.md, Flag form): one positive integer and one unit. A type
// named without its grammar is one grammar per tool, and a parameter file
// carrying a duration would mean different things to different readers of one
// field.
func units() []struct {
	suffix string
	unit   time.Duration
} {
	return []struct {
		suffix string
		unit   time.Duration
	}{
		{"ms", time.Millisecond},
		{"s", time.Second},
		{"m", time.Minute},
		{"h", time.Hour},
		{"d", 24 * time.Hour},
	}
}

// typeName is the type as -help prints it and as an error about a value names
// it. A list names its element, because "string" is true of a list of strings
// and useless to whoever has to type one.
func typeName(kind, elem Type, values []string) string {
	if kind == List {
		return "list of " + typeName(elem, String, values)
	}
	if kind == Enumeration {
		return "one of " + strings.Join(values, ", ")
	}
	return kind.String()
}

// convert turns one raw argument into the value the action receives, or reports
// what it expected. dir is the directory the tool was invoked from, which is
// what a path resolves against.
func convert(kind, elem Type, values []string, raw, dir string) (any, error) {
	switch kind {
	case String:
		return raw, nil

	case Path:
		if raw == "" {
			return nil, fmt.Errorf("%q is not a path", raw)
		}
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw), nil
		}
		return filepath.Clean(filepath.Join(dir, raw)), nil

	case Integer:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return n, nil

	case Duration:
		return parseDuration(raw)

	case Enumeration:
		for _, v := range values {
			if raw == v {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("%q is not one of %s", raw, strings.Join(values, ", "))

	case Boolean:
		// A boolean takes no value. Reaching here at all is a value someone
		// attached to one, and the caller names the spelling that exists.
		return nil, fmt.Errorf("a boolean takes no value")

	case List:
		return convertList(elem, values, strings.Split(raw, ","), dir)
	}
	return nil, fmt.Errorf("%q has no type", raw)
}

// convertList checks every element as its own type. An empty element is a usage
// error rather than a value silently dropped. The elements arrive already split
// — on commas from a command line, and as an array from a parameter file — so
// one list is one check whichever way it was written.
func convertList(elem Type, values []string, parts []string, dir string) (any, error) {
	switch elem {
	case Integer:
		out := make([]int64, 0, len(parts))
		for _, p := range parts {
			v, err := convertElement(elem, values, p, dir)
			if err != nil {
				return nil, err
			}
			out = append(out, v.(int64))
		}
		return out, nil
	case Duration:
		out := make([]time.Duration, 0, len(parts))
		for _, p := range parts {
			v, err := convertElement(elem, values, p, dir)
			if err != nil {
				return nil, err
			}
			out = append(out, v.(time.Duration))
		}
		return out, nil
	default:
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			v, err := convertElement(elem, values, p, dir)
			if err != nil {
				return nil, err
			}
			out = append(out, v.(string))
		}
		return out, nil
	}
}

// convertElement is one element of a list, with the empty-element rule the list
// adds to its element's own.
func convertElement(elem Type, values []string, raw, dir string) (any, error) {
	if raw == "" {
		return nil, fmt.Errorf("a list has no empty element")
	}
	return convert(elem, String, values, raw, dir)
}

// parseDuration reads one positive integer and one unit. No fraction, no sign,
// no compound — 1h30m is 90m — and no other unit.
func parseDuration(raw string) (any, error) {
	malformed := fmt.Errorf("%q is not a duration: one positive integer and one unit — ms, s, m, h, d", raw)
	for _, u := range units() {
		digits, ok := strings.CutSuffix(raw, u.suffix)
		if !ok || digits == "" {
			continue
		}
		// "1ms" must not read as 1 minute followed by a stray s, so the digits
		// are checked before the unit is believed.
		if !allDigits(digits) {
			continue
		}
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || n <= 0 {
			return nil, malformed
		}
		return time.Duration(n) * u.unit, nil
	}
	return nil, malformed
}

// allDigits reports whether s is one or more base-10 digits and nothing else.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
