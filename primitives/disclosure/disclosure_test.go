package disclosure

import (
	"strings"
	"testing"
)

// The seed rule, and the reason this is one function rather than a check per
// call site: the commit-identity check guarded the commit header for a long
// time, and nothing guarded the same address written into a file.
func TestPersonalEmailRefusedForEveryOrigin(t *testing.T) {
	for _, origin := range origins {
		if err := Allowed("contact "+fixtureEmail+" for details", origin); err == nil {
			t.Errorf("%s: a personal address was allowed", origin)
		}
	}
}

func TestNoreplyAddressAllowed(t *testing.T) {
	if err := Allowed("11466501+djabi"+NoreplyDomain, "operator"); err != nil {
		t.Errorf("the one publishable identity was refused: %v", err)
	}
}

// Documentation placeholders must pass. A guard that objects to example.com
// fires on prose, and a guard that fires on prose is one people disable — which
// is the failure mode, not the strictness.
func TestDocumentationPlaceholdersAllowed(t *testing.T) {
	for _, s := range []string{
		"e.g. alice@example.com",
		"user@example.org",
		"reach it at postmaster@example.net",
		"see http://127.0.0.1:9099 for the local runner",
		"install under /home/user/ or /Users/you/",
		"or /Users/runner/work on CI",
	} {
		if err := Allowed(s, "worktree"); err != nil {
			t.Errorf("%q was refused: %v", s, err)
		}
	}
}

func TestPrivateAddressRefused(t *testing.T) {
	for _, s := range []string{
		`"promise": "http://192.168` + `.1.7:9121"`,
		"ssh 10.0" + ".0.4",
		"172.16" + ".3.9",
		"172.31" + ".255.1",
		"http://build-box" + ".local",
	} {
		if err := Allowed(s, "worktree"); err == nil {
			t.Errorf("%q was allowed", s)
		}
	}
}

// Loopback names nothing about the network it is on and appears in every piece
// of local documentation.
func TestLoopbackAllowed(t *testing.T) {
	if err := Allowed("no arena context API at http://127.0.0.1:9099", "worktree"); err != nil {
		t.Errorf("loopback was refused: %v", err)
	}
}

func TestCredentialsRefused(t *testing.T) {
	for name, s := range map[string]string{
		"github pat":     "token " + fixturePAT,
		"fine-grained":   "github_pat_11" + "ABCDEFG0abcdefghijklmnop",
		"github oauth":   "gho_" + "abcdefghijklmnopqrstuvwxyz01",
		"openai style":   "sk-" + "abcdefghijklmnopqrstuvwxyz01",
		"anthropic":      "sk-ant-api03-" + "aaaaaaaaaaaaaaaaaaaaaaaa",
		"aws access key": "AKIA" + "IOSFODNN7EXAMPLE",
		"private key":    "-----BEGIN OPENSSH" + " PRIVATE KEY-----",
		"bare pem key":   "-----BEGIN" + " PRIVATE KEY-----",
		"slack":          "xoxb-" + "1234567890-abcdefghij",
	} {
		if err := Allowed(s, "agent"); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
}

// Every rule applies to every origin. Origin names the party behind a string,
// not the kind of string it is, so nothing may vary by it — and a rule that
// quietly did would be a rule switched off for one caller.
func TestRulesApplyToEveryOrigin(t *testing.T) {
	for _, origin := range origins {
		if err := Allowed(fixtureHome, origin); err == nil {
			t.Errorf("%s: a home path naming a real user was allowed", origin)
		}
	}
}

// An origin that cannot be stated is a refusal, not a default: a caller that
// will not say where text came from has not established that it may be
// published.
func TestUnstatedOriginRefused(t *testing.T) {
	for _, origin := range []string{"", " ", "\t\n"} {
		if err := Allowed("entirely ordinary text", origin); err == nil {
			t.Errorf("a blank origin %q was accepted", origin)
		}
	}
}

// An origin this package does not recognise IS accepted, and that is the
// deliberate difference from a closed set living here.
//
// Origin is carried into the refusal, never matched, so there is nothing here to
// validate against. A second enumeration would put a crosswalk between two
// closed sets in the one place a `default:` can turn an unrecognised member into
// a permitted one. A caller that owns a closed set validates its own membership
// before calling — which is where a value it owns belongs.
func TestUnrecognisedOriginIsCarriedNotJudged(t *testing.T) {
	if err := Allowed("entirely ordinary text", "a-caller-we-have-never-heard-of"); err != nil {
		t.Errorf("an unrecognised origin was refused: %v", err)
	}
	err := Allowed(fixtureEmail, "a-caller-we-have-never-heard-of")
	if err == nil {
		t.Fatal("an unrecognised origin skipped the rules")
	}
	if !strings.Contains(err.Error(), "a-caller-we-have-never-heard-of") {
		t.Errorf("the refusal does not carry the origin it was given: %v", err)
	}
}

// The refusal has to say what was found, or whoever hit it cannot tell a real
// secret from a false positive without guessing.
func TestRefusalNamesTheFinding(t *testing.T) {
	err := Allowed("key "+fixturePAT+" here", "worktree")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"ghp_", "credential", "worktree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// Ordinary prose must pass untouched. This is the test that fails first if a
// future rule is written against a topic rather than a shape.
func TestOrdinaryProseAllowed(t *testing.T) {
	for _, s := range []string{
		"Fixes the verify command so it stops printing JSON at a human.",
		"The runner appends --envelope; the gate prints one object on stdout.",
		"See docs/primitives.md for what belongs in this library.",
		"",
	} {
		if err := Allowed(s, "flow"); err != nil {
			t.Errorf("ordinary prose was refused: %q — %v", s, err)
		}
	}
}

// Every case below was a false positive the rules produced on a real tree. They
// are the reason the rules are checked against real content and not only against
// invented examples: each one would have refused a commit that discloses nothing.
func TestFalsePositivesFoundOnRealTrees(t *testing.T) {
	for what, s := range map[string]string{
		"an action pin is not an address":  "uses: contributor-assistant/github-action@v2.6.1",
		"a noreply trailer discloses none": "Co-Authored-By: Claude <noreply@anthropic.com>",
		"a hyphenated no-reply too":        "from no-reply@anthropic.com",
		"a provisioned filename":           "/.claude/settings.local.json",
		"another provisioned filename":     "/CLAUDE.local.md",
		"the docs' own home placeholder":   "logs      /home/dev/pr-pull/promise/.runner-governor.log",
		"a bare local filename":            `hookPath := filepath.Join(dir, "make.local")`,
		"an ssh remote is not a person":    "git@github.com:promise-language/forge.git",
	} {
		if err := Allowed(s, "worktree"); err != nil {
			t.Errorf("%s: %q was refused — %v", what, s, err)
		}
	}
}

// A host-taking command names its host in the argument straight after it, and
// that is all inHostPosition looks for. So an internal name in any LATER
// argument passes — `scp file db.internal` discloses a host and is allowed.
//
// This is recorded rather than fixed. The rules here are a faithful move of the
// implementation every caller already runs, and widening one during the move
// would make the moved copy disagree with the copies still in place — which is
// the drift this library exists to end. It is a real gap, it belongs in the
// backlog, and this test fails the moment it is closed, which is when the entry
// above should gain the case back.
func TestKnownMiss_InternalHostInALaterArgument(t *testing.T) {
	if err := Allowed("scp file db"+".internal", "worktree"); err == nil {
		return // the documented miss
	}
	t.Error("the later-argument miss has been closed — move this case back into " +
		"TestInternalHostStillRefusedWhenItEndsTheName and delete this test")
}

// The narrowing above must not cost the rules their teeth: a name that really
// does end in an internal suffix is still a host.
func TestInternalHostStillRefusedWhenItEndsTheName(t *testing.T) {
	for _, s := range []string{
		"http://tracker" + ".local:9121",
		"ssh build-box.internal",
		"db" + ".lan:5432",
		"user@host" + ".local",
	} {
		if err := Allowed(s, "worktree"); err == nil {
			t.Errorf("%q was allowed", s)
		}
	}
}
