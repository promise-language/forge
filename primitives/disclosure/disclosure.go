// Package disclosure decides whether text may be made public.
//
// It is the one implementation of that decision. Every path that sends data off
// the machine calls it: the workspace's tool-use guard over the resolved content
// of publishing commands (`gh issue create`, `gh pr create`, `curl -d`), the git
// pre-commit hook over staged file content, a tracker's file-a-bug path over the
// report it is about to submit, and the flow SDK over every string a write would
// send.
//
// It is NOT the tool-use guard. That one limits what an agent may DO, judging a
// tool call — a command, an edit path. This judges CONTENT, and it runs for
// people too.
//
// # Why one implementation
//
// The rules were written once and then copied, and the copy is what this package
// ends. A single rule — commit identities must be a noreply address — guarded the
// commit header while nothing guarded the same address written into a README, an
// issue body, or a commit message. Same data, same exposure, different call site.
// A check per call site is how that happens, and the only fix that holds is one
// function every call site reaches.
//
// # Origin is carried, never interpreted
//
// The caller names where the text came from, because provenance cannot be
// recovered from the text itself. No rule here reads it: every rule applies to
// every origin, and origin appears only in the refusal, so whoever is stopped
// learns which path was stopped.
//
// It is therefore a carried label rather than a closed set — what the guide
// keeps open is what a caller invents and the system only carries. Defining a
// second enumeration here would put a crosswalk between two closed sets in the
// one place a `default:` can quietly turn an unrecognised member into a
// permitted one. Callers that HAVE a closed set (the SDK's origins) pass its
// spelling and keep their own validation, which is where a value they own
// belongs.
//
// An origin that cannot be stated is still a refusal rather than a default: a
// caller that will not say where text came from has not established that it may
// be published.
//
// # No dependency, deliberately
//
// This package imports nothing outside the standard library, and that is a
// constraint rather than an accident. Every project's tools module depends on
// primitives, so anything primitives depends on is something every island tools
// module drags in — including the SDK's own tools module, which would then build
// against a published version of the SDK it is sitting inside.
//
// # It must stay fast
//
// The pre-commit caller runs on every commit over every staged file, and that
// hook's contract is sub-second: a slow hook gets disabled, and a disabled hook
// protects nothing. So the rules are string and pattern heuristics. No model
// call, no network, no read of anything outside the text it was given.
//
// # It is heuristic, and that is a deliberate trade
//
// These rules will miss things and will occasionally object to something
// harmless. The cost is asymmetric: a false negative publishes a secret, a false
// positive stops a commit and asks a person. So they lean toward refusing — but
// only on the SHAPE of the data, never on a topic, because a guard that objects
// to ordinary prose is one people learn to route around.
package disclosure

import (
	"fmt"
	"regexp"
	"strings"
)

// Allowed reports whether text may be made public. A nil return allows it; a
// non-nil error refuses and carries the reason, which is shown to whoever tried.
//
// There is no third answer. A caller that could read "maybe" would decide for
// itself, and the caller is the party being guarded.
//
// origin names the path the text came from — the SDK's origin spelling, a tool
// name, whatever the caller's own vocabulary is. It is reported, never matched.
func Allowed(text, origin string) error {
	// An origin that cannot be stated is a refusal, not a default. A caller
	// that accepted "" would be the place that made it one.
	if strings.TrimSpace(origin) == "" {
		return fmt.Errorf("disclosure refused: the caller named no origin for the text")
	}
	for _, r := range rules {
		if found := r.find(text); found != "" {
			return fmt.Errorf("disclosure refused (%s): %s — found %q", origin, r.why, found)
		}
	}
	return nil
}

// rule is one heuristic. find returns the offending substring, or "" to allow.
//
// Every rule applies to every origin. Scoping a rule away from an origin is a
// carve-out for a case that cannot be shown to be safe, and a scope that never
// changes an answer is a place for a future rule to be quietly switched off.
type rule struct {
	why  string
	find func(string) string
}

var rules = []rule{
	{
		// The seed rule, generalised. The commit-identity check has guarded the
		// commit header since before any of this; nothing guarded the same
		// address written into a README, a commit message, or an issue body.
		why:  "a personal email address must not reach public history",
		find: findPersonalEmail,
	},
	{
		why:  "an internal host or private address must not be published",
		find: findPrivateAddress,
	},
	{
		why:  "a credential must never be published",
		find: findCredential,
	},
	{
		// A home path is how a machine's user name leaks into a public
		// repository, usually via a pasted command or a baked-in absolute path.
		why:  "an absolute home path names the machine's user",
		find: findHomePath,
	},
}

// NoreplyDomain is the only email domain that may be published. It is the same
// rule the pre-commit identity check enforces, stated once here so the two
// cannot drift.
const NoreplyDomain = "@users.noreply.github.com"

// The final label must be alphabetic. Without that, `github-action@v2.6.1` in a
// workflow pin reads as an address — the first false positive the rules hit.
var emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)*\.[A-Za-z]{2,}`)

// findPersonalEmail returns the first address that is not a noreply or an
// obvious placeholder.
//
// example.com / example.org / localhost are exempt because they appear in
// documentation constantly and are reserved for exactly that. A rule that
// objected to them would fire on prose, which is how a guard gets disabled.
func findPersonalEmail(text string) string {
	for _, m := range emailRe.FindAllString(text, -1) {
		low := strings.ToLower(m)
		local, _, _ := strings.Cut(low, "@")
		switch {
		case strings.HasSuffix(low, NoreplyDomain):
		// An address whose local part is `noreply` discloses nobody by
		// construction.
		case local == "noreply", local == "no-reply":
		// `git@github.com` is the SSH remote convention. The local part is the
		// service account every clone uses; it names nobody.
		case local == "git":
		case strings.HasSuffix(low, "@example.com"), strings.HasSuffix(low, "@example.org"),
			strings.HasSuffix(low, "@example.net"), strings.HasSuffix(low, "@localhost"):
		default:
			return m
		}
	}
	return ""
}

// privateAddrRe matches RFC1918 addresses and .local / .internal hostnames.
//
// The hostname arm requires the suffix to END the name. A further dot-component
// means it is a filename, not a host: `settings.local.json` and `CLAUDE.local.md`
// are written by provisioning all over these trees, and a rule that called them
// internal hosts would fire on nearly every file that mentions it.
//
// Loopback is exempt: 127.0.0.1 names nothing about the network it is on, and it
// appears in every piece of local documentation.
var privateAddrRe = regexp.MustCompile(
	`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}` +
		`|192\.168\.\d{1,3}\.\d{1,3}` +
		`|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}` +
		`|[\w-]+\.(?:local|internal|lan))\b`)

// findPrivateAddress returns the first match whose suffix actually ends the
// name. Go's regexp is RE2, so that last condition cannot be a lookahead in the
// pattern — a further dot-component or word character is checked here instead.
func findPrivateAddress(text string) string {
	for _, loc := range privateAddrRe.FindAllStringIndex(text, -1) {
		if !endsTheName(text, loc[1]) {
			continue
		}
		m := text[loc[0]:loc[1]]
		if isInternalSuffixName(m) && !inHostPosition(text, loc[0], loc[1]) {
			continue
		}
		return m
	}
	return ""
}

func isInternalSuffixName(m string) bool {
	for _, suffix := range []string{".local", ".internal", ".lan"} {
		if strings.HasSuffix(m, suffix) {
			return true
		}
	}
	return false
}

// hostContextRe matches the commands that take a host as their argument. A name
// following one of these is being connected to, not named.
var hostContextRe = regexp.MustCompile(`(?:^|\s)(?:ssh|scp|sftp|ping|curl|wget|rsync|telnet|nc|mosh)\s+\S*$`)

// portRe matches a `:port` immediately after the name.
var portRe = regexp.MustCompile(`^:\d`)

// inHostPosition reports whether an internal-suffix NAME is being used as a host
// rather than as a filename.
//
// Unlike an RFC1918 address, which is an address wherever it appears, a name
// ending in .local is only a host in context: `make.local` and `secret.local`
// are files these repositories write and talk about constantly, and a rule that
// called them internal hosts would refuse a quarter of a tree. So this asks for
// the evidence a network name carries — a scheme, a port, or a command that
// takes a host.
//
// The cost is a real miss: a bare hostname sitting alone in prose passes. That
// is the deliberate side to err on, because the alternative fires on ordinary
// filenames, and a guard that fires on ordinary things gets disabled.
func inHostPosition(text string, start, end int) bool {
	before := text[:start]
	if strings.HasSuffix(before, "//") || strings.HasSuffix(before, "@") {
		return true
	}
	if portRe.MatchString(text[end:]) {
		return true
	}
	return hostContextRe.MatchString(before)
}

// endsTheName reports whether the match stops at a name boundary. It is what
// separates `tracker.local`, a host, from `settings.local.json`, a filename that
// provisioning writes all over these trees.
func endsTheName(text string, end int) bool {
	if end >= len(text) {
		return true
	}
	c := text[end]
	return c != '.' && c != '_' && c != '-' && !isWordByte(c)
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// credentialRes are shapes that are credentials wherever they appear. Prefixed
// tokens are matched by their issuer's own prefix, which is why they are worth
// having: the shape is unambiguous, so there is no false-positive cost.
var credentialRes = []*regexp.Regexp{
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}`),                   // GitHub personal access token
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),           // GitHub fine-grained PAT
	regexp.MustCompile(`\bgho_[A-Za-z0-9]{20,}`),                   // GitHub OAuth token
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`),                    // OpenAI-style secret key
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`),              // Anthropic key
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                     // AWS access key id
	regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----`), // any PEM private key
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),           // Slack token
}

func findCredential(text string) string {
	for _, re := range credentialRes {
		if m := re.FindString(text); m != "" {
			return m
		}
	}
	return ""
}

var homePathRe = regexp.MustCompile(`(?:/home/|/Users/)([\w.-]+)/`)

// findHomePath returns the matched path when it names a real-looking user.
// Placeholders that documentation uses are exempt for the same reason
// example.com is.
func findHomePath(text string) string {
	for _, m := range homePathRe.FindAllStringSubmatch(text, -1) {
		switch strings.ToLower(m[1]) {
		// `u` is a one-letter stand-in tests use. A single character cannot be
		// a real account worth protecting.
		case "user", "username", "you", "me", "someone", "runner", "dev", "u":
		default:
			return m[0]
		}
	}
	return ""
}
