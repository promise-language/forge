# Command-line library

The one implementation of [the CLI guide](../org/cli-guide.md) that every tool built from a
managed project's tools module is made from: what it models, what it makes true by construction,
and what is still left to the author of a tool.

**Out of scope:** what any particular tool does. The five tools every project builds — `make`,
`setup`, `verify`, `gate`, `run` — are [project-tools.md](project-tools.md); a project's other
commands are that project's. The product a project ships is not a tool built from its tools
module — the Promise compiler's `promise` is the case at hand — and this document does not reach
it.

---

## One implementation

> **Every tool built from a project's tools module parses its invocation, renders its help,
> selects its output mode, and chooses its exit status through `primitives/command`. A tool that
> reads `os.Args` for itself is a defect.**

The guide is mechanical. Whether `--json` and `-json` are one flag, whether an unknown flag is
refused, whether `-help` exits 0 — none of these is a place two projects could disagree and both
be right, which is the test [`primitives.md`](../primitives.md) applies to everything that belongs
in the shared library.

A parser written per tool disagrees with the guide in its own way, and the disagreements are the
kind nothing tests: an abbreviation accepted where the guide makes it unknown input, an output
mode taken from an environment variable the guide's Explicit inputs forbids, a flag refused by the
very mode that should honour it, an unknown flag ignored while the tool runs its whole pipeline
anyway.

None of those makes a test go red. A misspelled flag that is silently ignored does nothing while
appearing to work, so a parser defect is found by the person it misleads.

**The library is the guide's conformance suite as well as its implementation.** It carries one
test per rule, named by the guide's section, so an amendment to the guide is a change to the
library that is reviewed against those tests.

## The command tree

The root command is the binary. Every command has:

- a name, and a one-line summary;
- its flags and its positional parameters;
- **children**, an **action**, or both.

**Children are a closed set, whether they are declared or computed.** A declared set is written in
the tool's definition. A computed set is produced once per invocation from what the tool knows —
the gates a project answers, the commands it builds — and then treated exactly like a declared
one. A name outside it is unknown, which is the guide's Subcommands rule. Computing the set is
what lets `gate` and `run` address a vocabulary that is the project's, without a second copy of
that vocabulary anywhere.

**A command with children and no action requires a child.** Invoked without one, the invocation is
malformed, and the tool answers with [the brief form](#help-and-version): what this binary is, the
principal names it takes, and where the full list is. It goes to stderr, stdout stays empty, and
the status is 2 — so a script that ran the bare tool cannot read the output as a result.

**A delegating child hands everything after its name, verbatim, to another program.** `run verify
-json` runs `bin/verify` with `-json`. Nothing after the name is parsed, because none of it
belongs to the delegating tool — `-help` included, so `run verify -help` is `verify`'s help,
printed by `verify`. A delegating child therefore declares no flags of its own, not even the
[reserved ones](#names), which would otherwise claim a word meant for the delegate.

## Names

> **A name the library does not accept cannot be defined.** Every command name and every flag name
> is checked when the tool is defined, and a tool whose definition fails that check refuses to run,
> reporting every defect.

What the check can decide, it decides:

- **The alphabet** — lowercase ASCII letters, digits, and `-` as the only separator, from the
  guide's Flag form.
- **No alias.** The library has no way to give one flag a second name.
- **No collision**, including a boolean's unused spelling (below).

**A full English word is not mechanically checkable, and the library does not pretend to check
it.** `-ver` satisfies the alphabet and fails the guide. That rule is upheld by review, which is
where the guide's naming rules that a character class cannot express are upheld for every project.

Matching is exact: no prefix matching, no case folding. `--name` and `-name` are the same flag,
normalized once before matching.

**Some names are reserved, and no tool may define them:**

- `help`, on every command;
- `version`, on the root;
- `json` and `human`, on every command that writes a result;
- `json-input`, on every command that takes parameters;
- for every boolean a command declares, [the spelling it does not use](#types): a command with
  `-fetch` may not also define `-no-fetch`, and one with `-no-fetch` may not define `-fetch`.

**`-help`, `-version`, `-json` and `-human` are not boolean flags in the guide's sense, and they
carry no denial.** Output modes and Help and version define them as a request and a mode selector.
`-no-help` would be a flag that asks for nothing.

## Types

Every flag declares a type. `-help` prints the type, and an error about a value names the flag, the
value it was given, and the type it expected.

| Type | Accepts | Delivered to the action as |
|---|---|---|
| string | any text | `string` |
| path | text, resolved against the directory the tool was invoked from | an absolute, cleaned path |
| integer | a base-10 integer | `int64` |
| duration | one positive integer followed by `s`, `m` or `h` | `time.Duration` |
| enumeration | one member of a declared closed set | the enumeration's named type |
| boolean | the one spelling that changes it: `-name` when the default is off, `-no-name` when it is on. It takes no value | `bool` |
| names | comma-separated names | `[]string` |
| integers | comma-separated integers | `[]int64` |

**The guide names the first five types; the last three are this document's additions**, and each is
flagged rather than assumed. The two list types exist because real tools take lists — which arenas
to work, which tags to select — and the alternative is a string every command parses for itself,
which is the defect this library exists to remove. The duration's grammar is
[BASE's](https://github.com/promise-language/reactor/blob/main/docs/base-engineering.md#durations),
so that one duration spelling travels a command line, a parameter file and a manifest unchanged.
[Open questions](#open-questions) carries both.

**A path is resolved where the tool was invoked, never where it lives.** A tool knows its
repository from its build stamp ([project-tools.md#make](project-tools.md#make)), and a path
someone typed means what it would mean to any other program they ran from the same directory. The
two questions are distinct, and conflating them is how a tool comes to resolve a relative path
against a directory the person never named.

**A boolean has exactly one spelling, and it is the one that changes the default.** A default-off
boolean is `-name`, and a default-on boolean is `-no-name`. The opposite spelling is not defined,
and the parser refuses it as unknown input, naming the one that exists — a flag whose whole effect
is to assert the default asks for nothing, and it is a second way to say what saying nothing
already says.

**It takes no value.** `-name=true` and `-name=false` are refused, and the error names the spelling
that exists.

**A flag may be declared required.** A required flag that is absent is a usage error, and it is
reported along with every other problem the invocation has.

**Positional parameters are declared.** Each has a name, a type (any of the above except boolean),
and an arity: exactly one, one optional, or a trailing list. An argument that no declaration
accepts is an error naming that argument.

## Parsing

The order is the guide's One order. The library applies it in three steps.

1. **The command path.** Leading words that name a child of the current command descend the tree,
   and the path ends at the first word that does not. A word after the path's first flag that names
   a child is refused, because the path comes first.
2. **Flags.** A value is attached with `=` or given as the next argument. A flag after the first
   positional argument is refused.
3. **Positionals.** `--` ends the flags, and everything after it is positional, verbatim. A
   positional beginning with `-` is accepted only there.

> **Every problem with an invocation is reported, and nothing is done.** Unknown commands and
> flags, values that fail their type, missing required parameters, unexpected arguments, misplaced
> flags, contradictions, and conflicts with a parameter file are collected in one pass. Each is
> written to stderr on its own line, and the tool exits 2 before its action runs, which is the
> guide's Fail closed.

**An unknown name is reported with the nearest known name**, when one is within an edit distance of
two, together with the pointer to `-help`. The list of known names is not printed.

**`-help` is answered where the guide admits a flag**: after the resolved command path, before any
positional argument. It then answers that command's help and nothing else, exiting 0 even when
other flags in the invocation are wrong — a person who asked what the command takes is not served
by a list of their other mistakes. A `-help` written anywhere else is a misplaced flag like any
other, reported and refused with everything else.

**A contradiction the types cannot see is still found before the action runs.** A command may
declare a validation, and the library calls it after parsing. Every error it returns is reported in
the same pass and with the same exit status. `-json` and `-human` together is the contradiction the
library checks itself.

## Help and version

**`-help` writes to stdout and exits 0.** Help is generated from the definitions the parser uses,
so no hand-written usage text exists anywhere to drift from what a command accepts.

- **The root's help is the full surface.** It lists every command path, and under each command
  every flag with its type, its default, and its one-line description.
- **A computed set is described, not enumerated.** Help says what the set is and which invocation
  lists it, because the enumeration has one home and `-help` is not it
  ([tool-contract.md](https://github.com/promise-language/workspace/blob/main/docs/tool-contract.md),
  Required tools). [Open questions](#open-questions) carries this against the guide's Help and
  version.

**The brief form answers a bare invocation of a command that requires one, and it is not the
help.**

```
workspace 4f2a91c
expecting a subcommand: setup, update, doctor
run `workspace -help` for all of them
```

- **It names the binary and its version.** A reader who typed a name and stopped may also be
  holding the wrong build, and that costs one line.
- **It names the principal commands**, which the definition marks. A tool that marks none names all
  of them, which is the same answer while a tool is small.
- **It ends at the pointer to `-help`**, the one place the full surface appears.
- **It goes to stderr, with an empty stdout, and the invocation's status is 2.**

**Why not the whole help.** The reader here did not ask what the tool can do; they asked it to do
something and left a word out. The full surface buries the one fact they need, and a tool that
answers every mistake with its entire manual teaches people to skip the answer. Asking for help is
a different act, and it is answered in full, on stdout, with status 0.

**An unknown command is not this case.** It is named, with the nearest match, and the pointer to
`-help` — the brief form's list would read as the whole of what the tool takes.

**`-version` obeys the guide's ordering exactly as `-help` does**: it is accepted after whatever
command path resolved, and it answers about the binary, which is one answer however deep the path
went. It exits 0.

**It is a result like any other, and the guide's "one line" describes its human mode.** A version
is the answer most often read by a program — it is how one binary identifies another — so it
selects its mode exactly as every other result does: one line at a terminal, JSON when stdout is
not one, and `-json` or `-human` forcing either. An exemption would be the one case every caller
that pipes uniformly gets wrong.

```json
{"project": "workspace", "text": "v0.12.0", "major": 0, "minor": 12, "patch": 0}
{"project": "gate", "text": "9f1c2e7a5b0d4c31"}
```

- **`project` and `text` are separate**, and `text` carries the version without the project's name.
  A caller wanting one of the two facts never splits a string to get it, and a caller wanting the
  line composes it.
- **`major`, `minor` and `patch` are present when the version is semantic**, with `prerelease` and
  `build` when it carries them, so no consumer parses a version to compare one.
- **A version that is not semantic omits them rather than zeroing them.** A hash has no major, and
  `0` would be read as a real number by a comparison, where the guide has an absent field mean
  unknown. The second line above is a project tool, whose version is defined by
  [project-tools.md#staleness](project-tools.md#staleness).
- **The human mode is `<project> <text>`.**
- **A build that recorded no version says so**, and never reports a version of the empty string.

**`-help` and `-version` answer before anything else a tool does**, including
[the refusal](#exit-status-and-refusal). Both describe the binary rather than act on the tree, and
a stale binary's version is exactly the fact needed to diagnose it.

## Output

> **An action never writes to stdout. It returns a result, and the library writes that result once,
> whole, in the mode the invocation selected.**

- **The mode is the guide's Output modes**, applied by the library rather than by each tool:
  `-json` or `-human` forces it, and otherwise stdout being a character device decides.
- **A JSON result is one object, encoded before anything is written**, so a failure while encoding
  leaves stdout untouched. It is terminated by a newline.
- **A human result is the result's own rendering.** Each result type provides one.
- **Narration goes to stderr, through a writer the library gives the action.** An action has no
  other way to reach a stream.
- **A JSON result evolves additively**, as the guide requires of anything it calls a stable
  interface.

> **The human rendering is for a person, and a program reads the JSON.** A reader that parses the
> human form is a defect, whatever it parses today.

The human form is a rendering: its labels, its column widths, its order, and the very choice of one
name per line are made for whoever is reading, and they are improved for that reader without notice.
Nothing promised to keep them, so a program built on them breaks the first time the output gets
better, and it breaks silently — a parse that finds nothing looks exactly like a tool that answered
nothing. The JSON is the stable interface, it evolves additively, and a program that wants it asks
for it with `-json` rather than relying on a pipe being detected on its behalf.

**A command whose stdout is fixed by another contract declares itself JSON-only.** `gate <name>
--envelope` writes an envelope and `run <gate> --verdict` writes a verdict, and both shapes belong
to documents outside this one. `-human` on either is a contradiction and exits 2: there is no human
rendering to select, and a flag that was accepted and ignored is the silent-failure case Fail
closed exists to prevent. **This is the one place the library departs from the guide as written**,
whose Output modes makes `-human` unconditional, and [Open questions](#open-questions) carries it.

## Exit status and refusal

An action returns an outcome, and the library maps it to an exit status. No code path in a tool
calls `os.Exit` except its `main`, which exits with what the library returned. That keeps every
outcome testable without starting a process.

| Status | Outcome |
|---|---|
| `0` | Done: what was asked, including help, version, and an empty result. |
| `1` | Could not complete, or stopped on a condition a person must clear. |
| `2` | The invocation was malformed, and nothing was done. |
| `3` | **Refused:** the binary declined to run, and nothing was done. |

The first three are the guide's Exit codes. The fourth is this document's, and
[Open questions](#open-questions) carries it.

**A refusal is not a failure, and a caller must be able to tell the two apart without reading
prose.** A tool that exits 1 over a stale build has not measured the tree, has not built anything,
and has not judged anything. A caller that reports that as the tree's failure has made a claim
about a repository that was never put to the question, and has named a repair that is not the
repair. So a refusal has its own status, and — for a caller reading a pipe — its own object:

```json
{"refusal": "stale", "tool": "run", "detail": "tools source has changed since this binary was built", "recovery": ["./make"]}
```

| Field | Carries |
|---|---|
| `refusal` | Why this binary declined, from a closed set: `stale`, `unstamped`, `repository-unreachable`, `unbuilt`. What each names, and when a tool raises it, is [project-tools.md#staleness](project-tools.md#staleness). |
| `tool` | The name of the tool that refused. |
| `detail` | Prose for a person. Nothing keys on it. |
| `recovery` | The program and arguments that clear the condition, run from the repository root and exec'd, never interpreted: `["./make"]`, and `[".\\make.cmd"]` on Windows. |

- **In JSON mode the refusal object is the whole of stdout**, except in the two modes whose stdout
  another contract claims, where a refusal writes nothing at all
  ([project-tools.md#staleness](project-tools.md#staleness)).
- **In human mode stdout is empty.** In both modes, one line on stderr says the same thing.
- **The mode for a refusal is decided without the parser.** `-json` or `-human` anywhere in the
  arguments decides it. Otherwise `--envelope` and `--verdict` imply JSON. Otherwise stdout
  decides. A stale binary's parser may itself be the thing that changed, and a refusal must come
  out right however little of the binary can still be trusted.

> **A caller that receives the refusal status has received no result.** It runs the recovery from
> the repository root and asks again. It never reports the refusal as the outcome of the question
> it asked.

## Input from a file

Every command that takes parameters accepts `-json-input <path>`, and the file carries the same
closed set of parameters the command line does, which is the guide's Invocation from a file.

- **Keys are flag names**, plus `args`, an array of the positional arguments. The reserved flags
  are keys like any other.
- **Each value is applied through the converter the command line uses**, so a value legal in the
  file is legal on the command line and fails with the same error when it is not. JSON types map
  directly:
  - a boolean's key is its one spelling, and it takes `true` or `false`. `true` applies the flag;
    `false` is what not naming it means, and never reaches for the spelling that does not exist;
  - a list takes an array;
  - an integer must be a JSON number with no fractional part;
  - a duration is a string in the grammar [Types](#types) fixes.
- **An unknown key is an unknown flag**, reported like one.
- **A file that is absent, unreadable, or not a JSON object is a usage error** naming the path,
  reported with everything else wrong with the invocation.
- **A parameter set both in the file and on the command line is a usage error.** There is no
  precedence.
- **The file may not set `json-input`.** The key `help` means what `-help` means.
- **The file supplies one command's parameters, never the command path.** Which command runs is
  written on the command line, where a reader of the invocation can see it.
- **The path is resolved like any path parameter.**

## What a tool decides

The command tree, each command's summary, and which commands are principal — the ones
[the brief form](#help-and-version) names. Each flag's type, default, whether it is required, and
its description. The positional parameters. The result type and its human rendering. The validation
of contradictions the types cannot express.

**Two of those choices the library cannot make and review must**: that a name is a full English
word, and that a flag names the one thing it overrides rather than answering questions the tool has
not asked, which is the guide's No general switches.

Nothing else. The prefix, the order, the help layout, the selection of the output mode, the
refusal, and the exit status are not choices a tool makes.

**A tool's own tests call the library's definition check**, so a definition defect fails the
project's `tested` gate rather than the first invocation that reaches it.

## Constraints on the library

- **It imports nothing outside the standard library**, like the rest of
  [`primitives`](../primitives.md). Terminal detection asks whether stdout is a character device,
  and needs no terminal package.
- **It has no `init()` and no package-level mutable state.** Its streams, working directory and
  arguments are passed in, which is what lets every rule above be tested without a process.
- **It reads no environment variable**, neither for a parameter nor for the output mode. The two
  uses the guide's Explicit inputs sanctions are a tool's own debug diagnostics and a guard's
  containment markers, and the library needs neither.

## Open questions

**Whether a computed command name may carry a colon.** Gate names are `concept:instance`
([gates-and-commands.md](https://github.com/promise-language/flow/blob/main/docs/gates-and-commands.md),
The names), and the protocol's exec lines place a flag after the name: `bin/gate tested:root
--envelope`, `bin/run tested:root --verdict`. Under One order that is correct only if the name is
the command path, and Flag form's alphabet admits no colon. Two resolutions exist: admit it as the
separator of an instance in a computed command name, or re-spell the protocol as `bin/gate tested
root --envelope`, which changes the exec lines flow and base both define. **The recommendation is
the first**, and it is [org#16](https://github.com/promise-language/org/issues/16).

**Whether a boolean is a pair or a single spelling.** Flag form states the pair unconditionally:
`-my-flag` asserts and `-no-my-flag` denies. [Types](#types) defines only the spelling that changes
the default, because the other one asserts what silence already says, which the guide's own
one-name-per-flag rule and the engineering guide's one obvious way both refuse. **The
recommendation is the single spelling**, and it is
[org#5](https://github.com/promise-language/org/issues/5).

**Whether the guide gains a refusal status.** Exit codes defines three, so a tool that declined to
run and a tool that failed report the same number, and the distinction survives only as prose on
stderr. **The recommendation is the fourth status**, and it is
[org#17](https://github.com/promise-language/org/issues/17).

**Whether the guide admits a tool whose output another contract fixes.** Output modes requires
every tool to support `-human`, and Help and version requires `-help` to print every subcommand.
Neither has room for a command whose stdout is an envelope or a verdict, nor for a subcommand set
that is the project's gates rather than the tool's own vocabulary — which is why [Output](#output)
refuses `-human` on two commands and [Help and version](#help-and-version) describes a computed set
instead of listing it. **The recommendation is that the guide carve out both**, filed against
`org`.

**Whether the guide's type list is closed.** Flag form names string, integer, boolean, duration,
path and enumeration. [Types](#types) adds two list types and fixes the duration's grammar. **The
recommendation is that the guide name the list types and adopt one duration grammar**, filed
against `org`; a tool taking a list is otherwise written against this document rather than against
the guide.
