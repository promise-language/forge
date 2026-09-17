package command

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// applyJSONInput supplies a command's parameters from a JSON file whose keys
// map exactly to its flags, plus "args", the positional arguments
// (docs/command-line.md, Input from a file).
//
// The file is a transport for the same closed parameter set, not a second
// configuration system: every value is applied through the converter the
// command line uses, so a value legal in the file is legal on the command line
// and fails with the same error when it is not.
func (p *parsed) applyJSONInput(cur *resolved, path string, s Streams, wantJSON, wantHuman *bool, positionals *[]string) {
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("-%s %s cannot be read: %v", flagJSONInput, path, err))
		return
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		p.problems = append(p.problems, fmt.Sprintf("-%s %s is not a JSON object: %v", flagJSONInput, path, err))
		return
	}

	// The keys are read in one order whatever order they were written in, so
	// two runs over one file report the same problems in the same sequence.
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		raw := object[key]
		switch key {
		case flagJSONInput:
			p.problems = append(p.problems, fmt.Sprintf("%s: the file may not set -%s", path, flagJSONInput))
		case "args":
			var args []string
			if err := json.Unmarshal(raw, &args); err != nil {
				p.problems = append(p.problems, fmt.Sprintf("%s: \"args\" is an array of the positional arguments", path))
				continue
			}
			if len(*positionals) > 0 {
				p.problems = append(p.problems, p.bothWays(path, "the positional arguments"))
				continue
			}
			*positionals = args
		case flagHelp:
			if p.fileBool(path, key, raw) {
				p.help = true
			}
		case flagVersion:
			if p.fileBool(path, key, raw) {
				p.version = true
			}
		case flagJSON:
			if p.fileBool(path, key, raw) {
				*wantJSON = true
			}
		case flagHuman:
			if p.fileBool(path, key, raw) {
				*wantHuman = true
			}
		default:
			p.applyFileFlag(cur, path, key, raw, s.Dir)
		}
	}
}

// applyFileFlag applies one key that names one of the command's own flags.
func (p *parsed) applyFileFlag(cur *resolved, path, key string, raw json.RawMessage, dir string) {
	f := cur.flag(key)
	if f == nil {
		// An unknown key is an unknown flag, reported like one.
		p.problems = append(p.problems, p.unknown("flag", "-"+key, "-", cur, flagNames(cur)))
		return
	}
	// A parameter set both in the file and on the command line is a usage
	// error. There is no precedence, because precedence is a fallback.
	if p.call.given[key] {
		p.problems = append(p.problems, p.bothWays(path, "-"+key))
		return
	}

	if f.Type == Boolean {
		// true applies the flag; false is what not naming it means, and never
		// reaches for the spelling that does not exist.
		if p.fileBool(path, key, raw) {
			p.call.given[key] = true
		}
		return
	}

	if f.Type == List {
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			p.problems = append(p.problems, fmt.Sprintf("%s: %q takes an array, and it is a %s",
				path, key, typeName(f.Type, f.Elem, f.Values)))
			return
		}
		parts := make([]string, 0, len(elements))
		for _, element := range elements {
			spelling, ok := p.spelling(path, key, f.Elem, element)
			if !ok {
				return
			}
			parts = append(parts, spelling)
		}
		v, err := convertList(f.Elem, f.Values, parts, dir)
		if err != nil {
			p.problems = append(p.problems, fmt.Sprintf("%s: %q %s, and it takes a %s",
				path, key, err, typeName(f.Type, f.Elem, f.Values)))
			return
		}
		p.call.flags[key] = v
		p.call.given[key] = true
		return
	}

	spelling, ok := p.spelling(path, key, f.Type, raw)
	if !ok {
		return
	}
	v, err := convert(f.Type, f.Elem, f.Values, spelling, dir)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s: %q %s, and it takes a %s",
			path, key, err, typeName(f.Type, f.Elem, f.Values)))
		return
	}
	p.call.flags[key] = v
	p.call.given[key] = true
}

// spelling turns one JSON value into what the same parameter would have been
// spelled as on the command line, so that the one converter checks both.
func (p *parsed) spelling(path, key string, kind Type, raw json.RawMessage) (string, bool) {
	text := strings.TrimSpace(string(raw))
	if kind == Integer {
		// An integer must be a JSON number with no fractional part, rather than
		// a number that happens to round to one.
		if strings.ContainsAny(text, ".eE") || !json.Valid(raw) {
			p.problems = append(p.problems, fmt.Sprintf("%s: %q is %s, and it takes an integer with no fractional part",
				path, key, text))
			return "", false
		}
		return text, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s: %q is %s, and it takes a %s as a string",
			path, key, text, kind))
		return "", false
	}
	return s, true
}

// fileBool reads a key the file states as true or false, and reports whether it
// applies. Anything but a JSON boolean is a usage error naming the key.
func (p *parsed) fileBool(path, key string, raw json.RawMessage) bool {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s: %q takes true or false", path, key))
		return false
	}
	return b
}

// bothWays is the usage error for a parameter the file and the command line
// both set.
func (p *parsed) bothWays(path, what string) string {
	return fmt.Sprintf("%s gives %s, and so does the command line; there is no precedence between them", path, what)
}
