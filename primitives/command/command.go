// Package command is the one implementation of the CLI guide
// (docs/org/cli-guide.md) that every tool built from a managed project's tools
// module is made from: it parses the invocation, renders the help, selects the
// output mode and chooses the exit status. A tool that reads os.Args for itself
// is a defect (docs/command-line.md, One implementation).
//
// A tool declares what it is — the command tree, each command's summary, its
// flags and their types, the result and its human rendering — and decides
// nothing else. The prefix, the order, the help layout, the selection of the
// output mode, the refusal and the exit status are not choices a tool makes
// (What a tool decides).
//
// Nothing here calls os.Exit, reads an environment variable, holds
// package-level mutable state or runs an init(). Streams, arguments and the
// working directory arrive as parameters, which is what lets every rule be
// tested without starting a process (Constraints on the library).
package command

import (
	"io"
	"time"
)

// Type is what a flag or a positional parameter accepts (docs/command-line.md,
// Types). Every one of these is the guide's, and this package adds none of its
// own.
type Type int

const (
	// String accepts any text.
	String Type = iota
	// Path accepts text, resolved against the directory the tool was invoked
	// from and delivered absolute and cleaned. A path means what it would mean
	// to any other program run from the same directory, never what it would
	// mean where the binary lives.
	Path
	// Integer accepts a base-10 integer, delivered as int64.
	Integer
	// Duration accepts one positive integer and one unit — ms, s, m, h, d.
	Duration
	// Enumeration accepts one member of the closed set a flag declares in
	// Values.
	Enumeration
	// Boolean has exactly one spelling, the one that changes the default, and
	// it takes no value.
	Boolean
	// List accepts comma-separated elements, each checked as Elem.
	List
)

// String names the type as -help prints it and as an error about a value
// reports it. A list names its element type through typeName rather than here,
// because the element is the flag's and not the type's.
func (t Type) String() string {
	switch t {
	case String:
		return "string"
	case Path:
		return "path"
	case Integer:
		return "integer"
	case Duration:
		return "duration"
	case Enumeration:
		return "enumeration"
	case Boolean:
		return "boolean"
	case List:
		return "list"
	}
	return "unknown"
}

// Arity is how many arguments a positional parameter takes.
type Arity int

const (
	// One is exactly one argument, and its absence is a usage error.
	One Arity = iota
	// Optional is one argument or none.
	Optional
	// Trailing is every remaining argument, and it comes last.
	Trailing
)

// Flag is one named parameter of one command.
type Flag struct {
	// Name is the one spelling, without a prefix. A boolean's name is the
	// spelling that changes its default: name when the default is off, no-name
	// when it is on. The opposite spelling does not exist.
	Name string
	// Type is what the flag accepts.
	Type Type
	// Elem is the element type when Type is List.
	Elem Type
	// Values is the closed set when Type or Elem is Enumeration.
	Values []string
	// Default is the value in force when the flag is absent, as -help shows it.
	// A boolean states its default in its spelling and leaves this empty.
	Default string
	// Required makes the flag's absence a usage error, reported with every
	// other problem the invocation has.
	Required bool
	// Description is the one-line description -help prints.
	Description string
	// Protocol names the document that owns stdout when this flag is given —
	// "gate-contract.md" for gate's --envelope and run's --verdict. On such an
	// invocation the command has one mode: -json and -human are refused naming
	// the contract, and a refusal writes nothing to stdout at all
	// (docs/command-line.md, Output and Exit status and refusal).
	Protocol string
}

// Param is one positional parameter of one command.
type Param struct {
	// Name is what an error about the argument calls it.
	Name string
	// Type is what the argument accepts. A positional is never a boolean.
	Type Type
	// Elem is the element type when Type is List.
	Elem Type
	// Values is the closed set when Type or Elem is Enumeration.
	Values []string
	// Arity is how many arguments this parameter takes.
	Arity Arity
	// Description is the one-line description -help prints.
	Description string
}

// Action runs a command and returns what to write. It never writes to stdout:
// it returns a result, and the library writes that result once, whole, in the
// mode the invocation selected. Narration goes to Call.Narrate.
type Action func(*Call) (Result, error)

// Result is what an action returns. Its JSON encoding is the stable interface
// a program reads; Human is the rendering a person reads, and it is improved
// for that reader without notice.
type Result interface {
	Human(w io.Writer) error
}

// StatusResult is a result that reports an outcome of its own: a verify that
// failed, a gate over its cap. The result is still written — the caller asked a
// question and got an answer — and the status says the answer was no.
type StatusResult interface {
	Result
	ExitStatus() int
}

// Command is one node of the tree. The root command is the binary.
type Command struct {
	// Name is the command's one name. A subcommand's name may be compound:
	// two or more names joined by ":", which is one name and not a prefix.
	Name string
	// Summary is the one-line description of what this command does.
	Summary string
	// Principal marks a command the brief form names. A tool that marks none
	// names all of them.
	Principal bool
	// Flags are this command's own flags.
	Flags []Flag
	// Params are this command's positional parameters, in order.
	Params []Param
	// Action is what this command does. A command has children, an action, or
	// both.
	Action Action
	// SelectedBy names the boolean flag that selects this command's own action
	// where it also has children — gate --list beside the gates gate answers.
	// Without that flag and without a child, the invocation is malformed and
	// the tool answers with the brief form.
	SelectedBy string
	// Validate is a contradiction the types cannot see. The library calls it
	// after parsing, and every error it returns is reported in the same pass
	// and with the same exit status.
	Validate func(*Call) []error
	// Delegate hands everything after this command's name, verbatim, to
	// another program: it returns the argv to exec, or the refusal that says
	// why it cannot. Nothing after the name is parsed, -help included, so a
	// delegating command declares no flags of its own.
	Delegate func(*Call) ([]string, *Refusal)

	// Children produces this command's children. A declared set returns the
	// same commands every time; a computed set is produced once per invocation
	// from what the tool knows, and is then treated exactly like a declared
	// one. A name outside it is unknown either way.
	Children func() []Command
	// ChildFlags are the flags every child carries. Declaring them here rather
	// than on each computed child is what keeps one declaration for a set the
	// tool does not enumerate — and it is what lets a refusal know which flags
	// claim stdout without computing the set at all.
	ChildFlags []Flag
	// ChildParams are the positional parameters every child carries.
	ChildParams []Param
	// ChildAction is the action every child runs. It reads Call.Name to learn
	// which child was asked for.
	ChildAction Action
	// ChildValidate is the validation every child carries.
	ChildValidate func(*Call) []error
	// ChildClass is the pattern help prints for a computed set — "<gate>".
	// Setting it is what makes the set described rather than enumerated,
	// because the enumeration has one home and -help is not it.
	ChildClass string
	// ChildSummary is the one-line description of the class.
	ChildSummary string
	// EnumeratedBy is the invocation that lists the computed set —
	// "gate --list".
	EnumeratedBy string
}

// Tool is a binary's whole definition.
type Tool struct {
	// Project is the project the binary is built from, as its repository names
	// it. It is the "project" of the version payload and the name errors carry.
	Project string
	// Version is what -version reports as "text": a release version or the
	// commit hash. Empty means this build recorded no version, which -version
	// says rather than reporting a version of the empty string.
	Version string
	// Fit is asked whether this binary may act at all, before the command line
	// is read. A refusal answers every invocation, -help and -version included.
	// A tool that can never be unfit — the builder every refusal names — leaves
	// it nil.
	Fit func() *Refusal
	// Root is the tree.
	Root Command
}

// Streams are everything the library reads or writes, injected so that every
// rule above can be tested without starting a process.
type Streams struct {
	// In is the command's standard input.
	In io.Reader
	// Out carries the result and nothing else.
	Out io.Writer
	// Err carries narration, problems and the brief form.
	Err io.Writer
	// OutIsTerminal decides the output mode when neither -json nor -human did.
	OutIsTerminal bool
	// Dir is the directory the tool was invoked from, and the one a path
	// parameter resolves against.
	Dir string
}

// Call is one invocation, after it parsed. An action reads its parameters here
// and reaches a stream no other way.
type Call struct {
	// Path is the resolved command path, the root's name first.
	Path []string
	// Narrate is where progress goes. It is stderr, so that `tool > out.json`
	// and `tool -json 2>/dev/null` both behave.
	Narrate io.Writer
	// In is the command's standard input, for a command whose input is a
	// stream rather than a parameter.
	In io.Reader
	// Dir is the directory the tool was invoked from.
	Dir string

	flags  map[string]any
	given  map[string]bool
	params map[string]any
}

// Name is the last element of the command path: the child that was asked for.
func (c *Call) Name() string {
	if len(c.Path) == 0 {
		return ""
	}
	return c.Path[len(c.Path)-1]
}

// Given reports whether the invocation named this flag. A boolean is read this
// way: the declared spelling is the one that changes the default, so naming it
// is the whole of what it says.
func (c *Call) Given(name string) bool { return c.given[name] }

// Bool reports whether the flag was given, which for a boolean is its value.
func (c *Call) Bool(name string) bool { return c.given[name] }

// String returns a string, path or enumeration flag's value, or its default.
func (c *Call) String(name string) string {
	v, _ := c.flags[name].(string)
	return v
}

// Int returns an integer flag's value, or its default.
func (c *Call) Int(name string) int64 {
	v, _ := c.flags[name].(int64)
	return v
}

// Duration returns a duration flag's value, or its default.
func (c *Call) Duration(name string) time.Duration {
	v, _ := c.flags[name].(time.Duration)
	return v
}

// Strings returns a list flag's elements when they are text.
func (c *Call) Strings(name string) []string {
	v, _ := c.flags[name].([]string)
	return v
}

// Ints returns a list flag's elements when they are integers.
func (c *Call) Ints(name string) []int64 {
	v, _ := c.flags[name].([]int64)
	return v
}

// Arg returns a positional parameter's value.
func (c *Call) Arg(name string) string {
	v, _ := c.params[name].(string)
	return v
}

// Args returns a trailing positional parameter's values.
func (c *Call) Args(name string) []string {
	v, _ := c.params[name].([]string)
	return v
}

// resolved is one command with the children and flags an invocation sees: its
// own, plus whatever its parent attaches to every child. The tree is resolved
// once per invocation, so a computed set is produced once and then treated
// exactly like a declared one.
type resolved struct {
	cmd      Command
	flags    []Flag
	params   []Param
	action   Action
	validate func(*Call) []error
	path     []string
	children []*resolved
	computed bool
}

// resolve materializes the tree. A Children function is called once, here.
func resolve(c Command, parent *Command, path []string) *resolved {
	r := &resolved{
		cmd:      c,
		flags:    c.Flags,
		params:   c.Params,
		action:   c.Action,
		validate: c.Validate,
		path:     append(append([]string{}, path...), c.Name),
	}
	if parent != nil {
		r.computed = parent.ChildClass != ""
		r.flags = append(append([]Flag{}, c.Flags...), parent.ChildFlags...)
		r.params = append(append([]Param{}, c.Params...), parent.ChildParams...)
		if r.action == nil {
			r.action = parent.ChildAction
		}
		if r.validate == nil {
			r.validate = parent.ChildValidate
		}
	}
	if c.Children != nil {
		for _, child := range c.Children() {
			r.children = append(r.children, resolve(child, &c, r.path))
		}
	}
	return r
}

// child finds a child by exact name. Addressing is exact: no prefix matching,
// no case folding, and a compound name is one name rather than a prefix to
// search under.
func (r *resolved) child(name string) *resolved {
	for _, c := range r.children {
		if c.cmd.Name == name {
			return c
		}
	}
	return nil
}

// flag finds a declared flag by name.
func (r *resolved) flag(name string) *Flag {
	for i := range r.flags {
		if r.flags[i].Name == name {
			return &r.flags[i]
		}
	}
	return nil
}
