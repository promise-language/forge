package command

import (
	"fmt"
	"strings"
)

// parsed is one invocation after the command line was read. Every problem with
// it is collected, because validation is exhaustive: the operator who typed two
// things wrong learns both (docs/org/cli-guide.md, Fail closed).
type parsed struct {
	cmd      *resolved
	call     *Call
	mode     Mode
	help     bool
	version  bool
	brief    bool
	protocol string
	delegate []string
	problems []string
}

// parse applies the guide's One order in three steps: the command path, then
// the flags, then the positional arguments.
func parse(root *resolved, args []string, s Streams) *parsed {
	p := &parsed{mode: Human}
	call := &Call{
		Narrate: s.Err,
		In:      s.In,
		Dir:     s.Dir,
		flags:   map[string]any{},
		given:   map[string]bool{},
		params:  map[string]any{},
	}

	// 1. The command path. Leading words that name a child of the current
	//    command descend the tree, and the path ends at the first word that
	//    does not.
	cur := root
	i := 0
	for i < len(args) {
		a := args[i]
		if a == endOfFlagsMarker || isFlagArg(a) {
			break
		}
		child := cur.child(a)
		if child == nil {
			break
		}
		cur = child
		i++
		// A delegating command hands everything after its name, verbatim, to
		// another program. Nothing after the name is parsed, -help included,
		// because none of it belongs to the delegating tool.
		if cur.cmd.Delegate != nil {
			call.Path = cur.path
			p.cmd, p.call = cur, call
			p.delegate = append([]string{}, args[i:]...)
			return p
		}
	}
	call.Path = cur.path
	p.cmd, p.call = cur, call

	// 2. The flags, then 3. the positionals. `--` ends the flags, and a
	//    positional beginning with - is accepted only after it.
	var positionals []string
	var wantJSON, wantHuman bool
	var jsonInput string
	endOfFlags := false
	// A flag is late only with respect to an argument the command actually
	// takes. Where a command declares no positional parameter, the word that
	// ended the path is itself the problem — an unknown command, or one
	// argument more than the command takes — and the flag written after it is
	// exactly where One order puts it. Reporting both would tell whoever typed
	// `gate teste --envelope` to reorder an invocation whose order was right.
	takesArguments := len(cur.params) > 0

	for ; i < len(args); i++ {
		a := args[i]
		if !endOfFlags && a == endOfFlagsMarker {
			endOfFlags = true
			continue
		}
		if endOfFlags || !isFlagArg(a) {
			positionals = append(positionals, a)
			continue
		}

		name, value, hasValue := splitFlag(a)
		// A flag appearing after the first positional argument is an error,
		// never a positional: a parser that stops at the first non-flag and
		// hands the rest through untouched has silently reinterpreted the
		// invocation.
		if len(positionals) > 0 {
			if takesArguments {
				p.problems = append(p.problems, fmt.Sprintf(
					"-%s is written after the argument %q, and every flag comes before the positional arguments",
					name, positionals[0]))
			}
			if f := cur.flag(name); f != nil && f.Type != Boolean && !hasValue {
				i++
			}
			continue
		}

		switch name {
		case flagHelp:
			p.help = true
			continue
		case flagVersion:
			p.version = true
			continue
		case flagJSON:
			wantJSON = true
			continue
		case flagHuman:
			wantHuman = true
			continue
		case flagJSONInput:
			raw, ok := p.value(name, value, hasValue, args, &i)
			if !ok {
				continue
			}
			resolvedPath, err := convert(Path, String, nil, raw, s.Dir)
			if err != nil {
				p.problems = append(p.problems, fmt.Sprintf("-%s %s", name, err))
				continue
			}
			jsonInput = resolvedPath.(string)
			continue
		}

		f := cur.flag(name)
		if f == nil {
			// A boolean has one spelling, and it is the one that changes the
			// default. The opposite spelling asks for nothing, so it is unknown
			// input — and the error names the one that exists rather than
			// leaving the reader to guess which half of the pair was real.
			if other := cur.flag(denial(name)); other != nil && other.Type == Boolean {
				p.problems = append(p.problems, fmt.Sprintf(
					"unknown flag -%s; did you mean -%s? it is the one spelling that changes the default",
					name, other.Name))
				continue
			}
			// A value cannot belong to a flag that does not exist, so it is
			// left where it is and reported on its own terms rather than
			// swallowed by a guess about what the misspelling meant.
			p.problems = append(p.problems, p.unknown("flag", "-"+name, "-", cur, flagNames(cur)))
			continue
		}

		if f.Type == Boolean {
			if hasValue {
				p.problems = append(p.problems, fmt.Sprintf(
					"-%s takes no value: -%s is the one spelling that changes it", name, name))
				continue
			}
			call.given[name] = true
			continue
		}

		raw, ok := p.value(name, value, hasValue, args, &i)
		if !ok {
			continue
		}
		v, err := convert(f.Type, f.Elem, f.Values, raw, s.Dir)
		if err != nil {
			p.problems = append(p.problems, fmt.Sprintf("-%s %s, and it takes a %s",
				name, err, typeName(f.Type, f.Elem, f.Values)))
			continue
		}
		call.flags[name] = v
		call.given[name] = true
	}

	// A word after the path's first flag that names a child is refused, because
	// the path comes first.
	var kept []string
	for _, a := range positionals {
		if child := cur.child(a); child != nil {
			p.problems = append(p.problems, fmt.Sprintf(
				"%q is a command of %q, and a command path is written before every flag",
				a, strings.Join(cur.path, " ")))
			continue
		}
		kept = append(kept, a)
	}
	positionals = kept

	p.applyJSONInput(cur, jsonInput, s, &wantJSON, &wantHuman, &positionals)
	// -help and -version answer an invocation that carries nothing else. What
	// it leaves out is not a problem: the required parameters are required of
	// the invocation that runs the command, and this one does not run it.
	if p.help || p.version {
		p.refuseCompany(cur, positionals)
	} else {
		p.assignParams(cur, positionals, s.Dir)
		p.requireFlags(cur)
	}
	p.selectOutput(cur, wantJSON, wantHuman, s.OutIsTerminal)
	if p.help || p.version {
		return p
	}

	if len(p.problems) > 0 {
		return p
	}
	if cur.validate != nil {
		for _, err := range cur.validate(call) {
			p.problems = append(p.problems, err.Error())
		}
	}
	if len(p.problems) > 0 {
		return p
	}

	// A command with children and no action requires a child. Invoked without
	// one, the invocation is malformed, and the tool answers with the brief
	// form rather than the whole vocabulary.
	selected := cur.action != nil &&
		(cur.cmd.SelectedBy == "" || call.given[cur.cmd.SelectedBy])
	if !selected {
		p.brief = true
	}
	return p
}

// value reads a flag's value, attached with = or given as the next argument.
func (p *parsed) value(name, attached string, hasValue bool, args []string, i *int) (string, bool) {
	if hasValue {
		return attached, true
	}
	if *i+1 < len(args) {
		*i++
		return args[*i], true
	}
	p.problems = append(p.problems, fmt.Sprintf("-%s was given no value", name))
	return "", false
}

// unknown reports a name the tool does not have, with the nearest known name
// when one is within an edit distance of two, and the pointer to -help. The
// list of known names is not printed: a wall of definitions buries the one fact
// the operator needs, and -help is that list, one step away.
func (p *parsed) unknown(kind, given, prefix string, cur *resolved, known []string) string {
	problem := fmt.Sprintf("unknown %s %s", kind, given)
	if near, ok := nearest(strings.TrimPrefix(given, prefix), known); ok {
		problem += fmt.Sprintf("; did you mean %s%s?", prefix, near)
	}
	return problem + fmt.Sprintf("; run `%s -help` for what it takes", strings.Join(cur.path, " "))
}

// assignParams hands the positional arguments to the declared parameters, in
// order. An argument no declaration accepts is an error naming that argument.
func (p *parsed) assignParams(cur *resolved, positionals []string, dir string) {
	rest := positionals
	for _, param := range cur.params {
		switch param.Arity {
		case Trailing:
			values := make([]string, 0, len(rest))
			for _, raw := range rest {
				v, err := convert(param.Type, param.Elem, param.Values, raw, dir)
				if err != nil {
					p.problems = append(p.problems, fmt.Sprintf("%s: %s, and it takes a %s",
						param.Name, err, typeName(param.Type, param.Elem, param.Values)))
					continue
				}
				values = append(values, fmt.Sprint(v))
			}
			p.call.params[param.Name] = values
			rest = nil
		case Optional, One:
			if len(rest) == 0 {
				if param.Arity == One {
					p.problems = append(p.problems, fmt.Sprintf("%s is required, and no argument gave it", param.Name))
				}
				continue
			}
			v, err := convert(param.Type, param.Elem, param.Values, rest[0], dir)
			if err != nil {
				p.problems = append(p.problems, fmt.Sprintf("%s: %s, and it takes a %s",
					param.Name, err, typeName(param.Type, param.Elem, param.Values)))
			} else {
				p.call.params[param.Name] = v
			}
			rest = rest[1:]
		}
	}
	for _, extra := range rest {
		// A command that takes children reads a stray word as a command name,
		// because that is what it would have been one position earlier.
		if len(cur.children) > 0 {
			p.problems = append(p.problems, p.unknown("command", extra, "", cur, childNames(cur)))
			continue
		}
		p.problems = append(p.problems, fmt.Sprintf("%q is one argument more than %q takes",
			extra, strings.Join(cur.path, " ")))
	}
}

// refuseCompany reports everything an invocation asking for -help or -version
// carries besides -json and -human. The operator who typed `tool sync -help
// origin` may have meant the help and may have meant the command, and guessing
// is worse than either answer (docs/org/cli-guide.md, Help and version).
//
// It reports presence, never correctness: a flag that was unknown, misplaced or
// given a value of the wrong type is already in the problems from the one pass,
// so what is left to name here is the company that parsed cleanly.
func (p *parsed) refuseCompany(cur *resolved, positionals []string) {
	// Each of the two says what it answers, in the words its own description
	// uses: -help prints what the command takes, -version what the binary is.
	asked, answers := "-"+flagHelp, "what the command takes"
	if p.version {
		asked, answers = "-"+flagVersion, "what this binary is"
	}
	refuse := func(what string) {
		p.problems = append(p.problems, fmt.Sprintf(
			"%s is not accepted with %s, which prints %s and does nothing else",
			what, asked, answers))
	}
	if p.help && p.version {
		p.problems = append(p.problems, fmt.Sprintf(
			"-%s and -%s ask for two answers: pass one", flagHelp, flagVersion))
	}
	for _, f := range cur.flags {
		if p.call.given[f.Name] {
			refuse("-" + f.Name)
		}
	}
	for _, extra := range positionals {
		refuse(fmt.Sprintf("%q", extra))
	}
}

// requireFlags reports every required flag the invocation left out.
func (p *parsed) requireFlags(cur *resolved) {
	for _, f := range cur.flags {
		if f.Required && !p.call.given[f.Name] {
			p.problems = append(p.problems, fmt.Sprintf("-%s is required, and the invocation does not give it", f.Name))
		}
	}
}

// selectOutput decides the mode, and refuses the mode flags on a command whose
// stdout a named contract fixes: there is no human rendering to select, and a
// flag that was accepted and ignored is the silent failure Fail closed exists
// to prevent.
func (p *parsed) selectOutput(cur *resolved, wantJSON, wantHuman, outIsTerminal bool) {
	for _, f := range cur.flags {
		if f.Protocol != "" && p.call.given[f.Name] {
			p.protocol = f.Protocol
		}
	}
	if p.protocol != "" {
		for _, name := range []string{flagJSON, flagHuman} {
			if (name == flagJSON && wantJSON) || (name == flagHuman && wantHuman) {
				p.problems = append(p.problems, fmt.Sprintf(
					"-%s is not a mode this command has: its output is fixed by %s", name, p.protocol))
			}
		}
		p.mode = JSON
		return
	}
	mode, err := selectMode(wantJSON, wantHuman, outIsTerminal)
	if err != nil {
		p.problems = append(p.problems, err.Error())
	}
	p.mode = mode
}

// flagNames is every flag name this command answers, the library's included,
// for the nearest-name suggestion.
func flagNames(cur *resolved) []string {
	names := make([]string, 0, len(cur.flags)+len(reserved()))
	for _, f := range cur.flags {
		names = append(names, f.Name)
	}
	return append(names, reserved()...)
}

// childNames is every child this command has, for the nearest-name suggestion.
func childNames(cur *resolved) []string {
	names := make([]string, 0, len(cur.children))
	for _, c := range cur.children {
		names = append(names, c.cmd.Name)
	}
	return names
}

// endOfFlagsMarker ends the flags. Everything after it is positional, verbatim.
const endOfFlagsMarker = "--"

// isFlagArg reports whether an argument is a flag. A lone "-" is not: it names
// no flag, and a name is what a flag is.
func isFlagArg(a string) bool {
	return len(a) > 1 && a[0] == '-' && a != endOfFlagsMarker
}

// splitFlag normalizes the prefix once and splits a value attached with =.
// --my-flag and -my-flag are one flag; the tools normalize the prefix once,
// then match the name exactly.
func splitFlag(a string) (name, value string, hasValue bool) {
	a = strings.TrimPrefix(a, "-")
	a = strings.TrimPrefix(a, "-")
	name, value, hasValue = strings.Cut(a, "=")
	return name, value, hasValue
}

// nearest is the closest known name within an edit distance of two, which is
// the distance at which a suggestion is a typo rather than a guess.
func nearest(given string, known []string) (string, bool) {
	best, bestDistance := "", 3
	for _, k := range known {
		if d := distance(given, k); d < bestDistance {
			best, bestDistance = k, d
		}
	}
	return best, best != ""
}

// distance is the Levenshtein distance between two names.
func distance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
