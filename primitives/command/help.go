package command

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// helpResult is the surface as data: the same facts in both modes, in the one
// object every tool shares (docs/org/cli-guide.md, Help and version). It is
// generated from the definitions the parser uses, so no hand-written usage text
// exists anywhere to drift from what a command accepts.
type helpResult struct {
	Project     string        `json:"project"`
	Description string        `json:"description"`
	Flags       []helpFlag    `json:"flags"`
	Arguments   []helpParam   `json:"arguments,omitempty"`
	Commands    []helpCommand `json:"commands"`

	path []string
}

// helpFlag is one flag, with the type -help prints and the default in force.
type helpFlag struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Element     string `json:"element,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description"`

	spelledType string
}

// helpParam is one positional parameter, as the usage line spells it and the
// arguments block describes it. The guide's object names a tool's flags and its
// commands; a command that takes an argument says so here, additively.
type helpParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Arity       string `json:"arity"`
	Description string `json:"description"`

	spelled string
}

// helpCommand is one child. A set the tool does not author is one entry whose
// name is the pattern and whose enumerated-by is the invocation that lists it,
// because the enumeration has one home and -help is not it.
type helpCommand struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	EnumeratedBy string     `json:"enumerated-by,omitempty"`
	Flags        []helpFlag `json:"flags"`
}

// helpFor renders one command's help.
func helpFor(t Tool, r *resolved) helpResult {
	h := helpResult{
		Project:     t.Project,
		Description: r.cmd.Summary,
		Flags:       describeFlags(r.flags, true, len(r.path) == 1),
		Arguments:   describeParams(r.params),
		Commands:    []helpCommand{},
		path:        r.path,
	}
	if r.cmd.ChildClass != "" {
		// A computed set is described, not enumerated: help says what the set
		// is and which invocation lists it, because the enumeration has one
		// home and -help is not it.
		h.Commands = append(h.Commands, helpCommand{
			Name:         r.cmd.ChildClass,
			Description:  r.cmd.ChildSummary,
			EnumeratedBy: r.cmd.EnumeratedBy,
			Flags:        describeFlags(r.cmd.ChildFlags, false, false),
		})
		return h
	}
	for _, c := range r.children {
		h.Commands = append(h.Commands, helpCommand{
			Name:        c.cmd.Name,
			Description: c.cmd.Summary,
			Flags:       describeFlags(c.flags, false, false),
		})
	}
	return h
}

// describeFlags lists a command's own flags and, where the command is one a
// reader can invoke, the library's too — they are flags that command answers
// like any other.
func describeFlags(flags []Flag, withLibrary, isRoot bool) []helpFlag {
	described := make([]helpFlag, 0, len(flags)+len(reserved()))
	for _, f := range flags {
		described = append(described, helpFlag{
			Name:        f.Name,
			Type:        f.Type.String(),
			Element:     element(f),
			Default:     f.Default,
			Required:    f.Required,
			Description: f.Description,
			spelledType: typeName(f.Type, f.Elem, f.Values),
		})
	}
	if !withLibrary {
		return described
	}
	described = append(described,
		library(flagHelp, "print this help and do nothing else"),
		library(flagJSON, "write the result as JSON, whatever stdout is"),
		library(flagHuman, "write the result for a person, whatever stdout is"),
	)
	if isRoot {
		described = append(described, library(flagVersion, "print what this binary is and do nothing else"))
	}
	described = append(described, helpFlag{
		Name:        flagJSONInput,
		Type:        Path.String(),
		Description: "read these parameters from a JSON file",
		spelledType: Path.String(),
	})
	return described
}

// describeParams describes each positional parameter the command declares.
func describeParams(params []Param) []helpParam {
	described := make([]helpParam, 0, len(params))
	for _, p := range params {
		described = append(described, helpParam{
			Name:        p.Name,
			Type:        p.Type.String(),
			Arity:       arity(p.Arity),
			Description: p.Description,
			spelled:     typeName(p.Type, p.Elem, p.Values),
		})
	}
	return described
}

// arity is how many arguments a parameter takes, as help says it.
func arity(a Arity) string {
	switch a {
	case Optional:
		return "optional"
	case Trailing:
		return "trailing"
	}
	return "one"
}

// spell is the parameter as the usage line writes it.
func spell(p helpParam) string {
	switch p.Arity {
	case "optional":
		return "[<" + p.Name + ">]"
	case "trailing":
		return "<" + p.Name + ">..."
	}
	return "<" + p.Name + ">"
}

// library is one of the flags this package answers rather than a tool.
func library(name, description string) helpFlag {
	return helpFlag{
		Name:        name,
		Type:        Boolean.String(),
		Description: description,
		spelledType: Boolean.String(),
	}
}

// element names a list's element type, and nothing for anything else.
func element(f Flag) string {
	if f.Type != List {
		return ""
	}
	return f.Elem.String()
}

// Human is the rendering a person reads: the command paths, and under each one
// every flag with its type, its default and its one-line description.
func (h helpResult) Human(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", strings.Join(h.path, " "), h.Description)

	fmt.Fprintf(&b, "Usage:\n  %s", strings.Join(h.path, " "))
	if len(h.Commands) > 0 {
		fmt.Fprintf(&b, " <command>")
	}
	fmt.Fprintf(&b, " [flags]")
	for _, p := range h.Arguments {
		fmt.Fprintf(&b, " %s", spell(p))
	}
	b.WriteString("\n")

	if len(h.Commands) > 0 {
		b.WriteString("\nCommands:\n")
		table := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, c := range h.Commands {
			description := c.Description
			if c.EnumeratedBy != "" {
				description += fmt.Sprintf(" — `%s` names them", c.EnumeratedBy)
			}
			fmt.Fprintf(table, "  %s\t\t%s\n", c.Name, description)
			for _, f := range c.Flags {
				fmt.Fprintf(table, "    -%s\t%s\t%s\n", f.Name, f.spelledType, describe(f))
			}
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}

	if len(h.Arguments) > 0 {
		b.WriteString("\nArguments:\n")
		table := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, p := range h.Arguments {
			fmt.Fprintf(table, "  %s\t%s\t%s\n", p.Name, p.spelled, p.Description)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}

	if len(h.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		table := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, f := range h.Flags {
			fmt.Fprintf(table, "  -%s\t%s\t%s\n", f.Name, f.spelledType, describe(f))
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// describe is one flag's description, with the default in force and whether it
// is required. Its type is a column of its own.
func describe(f helpFlag) string {
	line := f.Description
	if f.Default != "" {
		line += fmt.Sprintf(" (default %s)", f.Default)
	}
	if f.Required {
		line += " (required)"
	}
	return line
}

// briefForm answers a bare invocation of a command that requires one, and it is
// not the help (docs/command-line.md, Help and version).
//
// The version comes first because a reader who typed a name and stopped may
// also be holding the wrong build, and that is the first thing a bug report
// needs (docs/org/cli-guide.md, Help and version). Then the missing word and
// the names that would supply it, then where the full surface lives — and the
// full surface itself stays one flag away, because a tool that answers every
// mistake with its entire manual teaches people to skip the answer.
func briefForm(t Tool, r *resolved) string {
	var b strings.Builder
	if err := versionOf(t).Human(&b); err != nil {
		return b.String()
	}
	fmt.Fprintf(&b, "expecting a subcommand: %s\n", principalNames(r))
	fmt.Fprintf(&b, "run `%s -help` for all of them\n", strings.Join(r.path, " "))
	return b.String()
}

// principalNames is what the brief form offers: the commands the definition
// marks as principal, all of them where it marks none, and the class with the
// invocation that lists it where the set is the project's rather than the
// tool's.
func principalNames(r *resolved) string {
	if r.cmd.ChildClass != "" {
		return fmt.Sprintf("%s, %s — `%s` names them",
			r.cmd.ChildClass, r.cmd.ChildSummary, r.cmd.EnumeratedBy)
	}
	var principal, all []string
	for _, c := range r.children {
		all = append(all, c.cmd.Name)
		if c.cmd.Principal {
			principal = append(principal, c.cmd.Name)
		}
	}
	if len(principal) > 0 {
		return strings.Join(principal, ", ")
	}
	return strings.Join(all, ", ")
}
