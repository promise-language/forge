package command

import (
	"fmt"
	"strings"
)

// The names no tool may define, because the library answers them
// (docs/command-line.md, Names). A tool that defines one is a definition
// defect rather than a flag that quietly never fires.
const (
	flagHelp      = "help"
	flagVersion   = "version"
	flagJSON      = "json"
	flagHuman     = "human"
	flagJSONInput = "json-input"
)

// reserved is every name above, in the order an error should list them. It is a
// function rather than a package-level slice because a slice at package level is
// mutable, and this package holds no state one invocation could leave for the
// next (docs/command-line.md, Constraints on the library).
func reserved() []string {
	return []string{flagHelp, flagVersion, flagJSON, flagHuman, flagJSONInput}
}

// Check reports every defect in a tool's definition. A name the library does
// not accept cannot be defined, and a tool whose definition fails this check
// refuses to run rather than discovering the defect on the first invocation
// that reaches it — which is why a tool's own tests call it (docs/command-line.md,
// Names and What a tool decides).
//
// What the check can decide, it decides. That a name is a full English word it
// cannot: -ver satisfies the alphabet and fails the guide, and that rule is
// upheld by review.
func Check(t Tool) []error {
	return check(t, resolve(t.Root, nil, nil))
}

// check is Check over a tree that is already resolved, so that an invocation
// computes a command set once rather than once per reader of it.
func check(t Tool, root *resolved) []error {
	var problems []error
	if t.Project == "" {
		problems = append(problems, fmt.Errorf("the tool names no project, so -version has nothing to report"))
	}
	return append(problems, checkCommand(root, true)...)
}

// checkCommand walks one command and everything under it.
func checkCommand(r *resolved, isRoot bool) []error {
	var problems []error
	where := strings.Join(r.path, " ")

	if isRoot {
		if !validName(r.cmd.Name) {
			problems = append(problems, fmt.Errorf("%q is not a name: lowercase letters, digits and - as the only separator", r.cmd.Name))
		}
	} else if !validCommandName(r.cmd.Name) {
		problems = append(problems, fmt.Errorf("%q is not a command name: lowercase letters, digits, - as the only separator, and : joining a compound name", r.cmd.Name))
	}
	if r.cmd.Summary == "" {
		problems = append(problems, fmt.Errorf("%s: no summary, and -help prints one per command", where))
	}
	if r.action == nil && len(r.children) == 0 && r.cmd.Children == nil && r.cmd.Delegate == nil {
		problems = append(problems, fmt.Errorf("%s: neither children nor an action", where))
	}
	if r.cmd.Delegate != nil && len(r.flags) > 0 {
		problems = append(problems, fmt.Errorf("%s: a delegating command declares no flags — every word after its name belongs to the delegate", where))
	}
	if r.cmd.ChildClass != "" && r.cmd.EnumeratedBy == "" {
		problems = append(problems, fmt.Errorf("%s: a described command set names the invocation that enumerates it", where))
	}

	problems = append(problems, checkFlags(r, isRoot, where)...)
	problems = append(problems, checkParams(r, where)...)

	seen := map[string]bool{}
	for _, c := range r.children {
		if seen[c.cmd.Name] {
			problems = append(problems, fmt.Errorf("%s: two children named %q", where, c.cmd.Name))
		}
		seen[c.cmd.Name] = true
		problems = append(problems, checkCommand(c, false)...)
	}
	return problems
}

// checkFlags applies the alphabet, the reserved names, the collisions and a
// boolean's unused spelling to one command's flags.
func checkFlags(r *resolved, isRoot bool, where string) []error {
	var problems []error
	seen := map[string]bool{}
	for _, f := range r.flags {
		if !validName(f.Name) {
			problems = append(problems, fmt.Errorf("%s: %q is not a flag name: lowercase letters, digits and - as the only separator", where, f.Name))
		}
		if isReserved(f.Name, isRoot) {
			problems = append(problems, fmt.Errorf("%s: -%s is the library's, and no tool defines it", where, f.Name))
		}
		if seen[f.Name] {
			problems = append(problems, fmt.Errorf("%s: two flags named -%s", where, f.Name))
		}
		seen[f.Name] = true
		if f.Type == Enumeration && len(f.Values) == 0 {
			problems = append(problems, fmt.Errorf("%s: -%s is an enumeration over no members", where, f.Name))
		}
		if f.Type == List && (f.Elem == List || f.Elem == Boolean) {
			problems = append(problems, fmt.Errorf("%s: -%s is a list of %s, which is not an element type", where, f.Name, f.Elem))
		}
		if f.Type == List && f.Elem == Enumeration && len(f.Values) == 0 {
			problems = append(problems, fmt.Errorf("%s: -%s is a list of enumeration over no members", where, f.Name))
		}
		if f.Type == Boolean && f.Default != "" {
			problems = append(problems, fmt.Errorf("%s: -%s states a default, where a boolean's spelling is its default", where, f.Name))
		}
		if f.Type == Boolean && f.Required {
			problems = append(problems, fmt.Errorf("%s: -%s is a required boolean, which is a flag with one legal invocation", where, f.Name))
		}
	}
	// A boolean has one spelling, and it is the one that changes the default.
	// The opposite spelling is not defined, and the parser refuses it as
	// unknown input naming the one that exists.
	for _, f := range r.flags {
		if f.Type != Boolean {
			continue
		}
		if seen[denial(f.Name)] {
			problems = append(problems, fmt.Errorf("%s: -%s and -%s are the two spellings of one boolean, and only the one that changes the default exists", where, f.Name, denial(f.Name)))
		}
	}
	if s := r.cmd.SelectedBy; s != "" {
		f := r.flag(s)
		switch {
		case f == nil:
			problems = append(problems, fmt.Errorf("%s: its action is selected by -%s, which it does not declare", where, s))
		case f.Type != Boolean:
			problems = append(problems, fmt.Errorf("%s: its action is selected by -%s, which is a %s rather than a boolean", where, s, f.Type))
		case r.action == nil:
			problems = append(problems, fmt.Errorf("%s: -%s selects an action it does not have", where, s))
		}
	}
	return problems
}

// checkParams applies the arity rules to one command's positional parameters.
func checkParams(r *resolved, where string) []error {
	var problems []error
	seen := map[string]bool{}
	for i, p := range r.params {
		if !validName(p.Name) {
			problems = append(problems, fmt.Errorf("%s: %q is not a parameter name", where, p.Name))
		}
		if seen[p.Name] {
			problems = append(problems, fmt.Errorf("%s: two parameters named %q", where, p.Name))
		}
		seen[p.Name] = true
		if p.Type == Boolean {
			problems = append(problems, fmt.Errorf("%s: %q is a boolean positional, and a boolean is a flag", where, p.Name))
		}
		if p.Type == Enumeration && len(p.Values) == 0 {
			problems = append(problems, fmt.Errorf("%s: %q is an enumeration over no members", where, p.Name))
		}
		if p.Arity == Trailing && i != len(r.params)-1 {
			problems = append(problems, fmt.Errorf("%s: %q takes the trailing arguments and is not last", where, p.Name))
		}
		if p.Arity == One && i > 0 && r.params[i-1].Arity == Optional {
			problems = append(problems, fmt.Errorf("%s: %q is required after the optional %q, so no argument can reach it", where, p.Name, r.params[i-1].Name))
		}
	}
	return problems
}

// isReserved reports whether a tool may not define this flag name here.
// version is the root's alone; the rest are every command's.
func isReserved(name string, isRoot bool) bool {
	for _, r := range reserved() {
		if name != r {
			continue
		}
		if r == flagVersion {
			return isRoot
		}
		return true
	}
	return false
}

// denial is the spelling a boolean does not have: -no-fetch for -fetch, and
// -fetch for -no-fetch.
func denial(name string) string {
	if rest, ok := strings.CutPrefix(name, "no-"); ok {
		return rest
	}
	return "no-" + name
}

// validName reports whether s is a name: lowercase ASCII letters and digits,
// with - as the only separator within it (docs/org/cli-guide.md, Flag form).
// Case is the cheapest way to mint an accidental alias, and anything beyond
// lowercase ASCII types differently across keyboards.
func validName(s string) bool {
	if s == "" {
		return false
	}
	afterDash := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			afterDash = false
		case c == '-':
			if afterDash {
				return false
			}
			afterDash = true
		default:
			return false
		}
	}
	return !afterDash
}

// validCommandName reports whether s is a command name, which may additionally
// be compound: two or more names joined by ":", which is one name and not a
// prefix to search under. An empty segment is not a name.
func validCommandName(s string) bool {
	if s == "" {
		return false
	}
	for _, segment := range strings.Split(s, ":") {
		if !validName(segment) {
			return false
		}
	}
	return true
}
