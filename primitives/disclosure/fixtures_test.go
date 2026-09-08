package disclosure

// Detector fixtures, assembled rather than spelled.
//
// These rules are about what reaches public history AS TEXT. A literal here
// would put a personal-looking address and a credential-shaped token into every
// clone of this repository — and, because the pre-commit hook checks staged
// content through these very rules, would refuse the commit that carried its own
// test suite.
//
// The concatenation is what makes the difference: the email rule needs the local
// part, the "@" and the domain contiguous, and the credential rule needs its
// prefix followed immediately by the run of characters. Split across a "+",
// neither matches the file. Joined at runtime, both match the value.
var (
	fixtureEmail = "real.person" + "@" + "gmail.com"
	fixturePAT   = "ghp_" + "abcdefghijklmnopqrstuvwxyz012345"
	fixtureHome  = "ws='/home/" + "djabi/prog/workspace'"
)

// origins are representative caller labels. They are not a closed set this
// package owns — that is the point of the type being a string — so the list
// exists only to show that the answer never depends on which one is passed.
var origins = []string{"agent", "operator", "worktree", "flow", "tracker-file-a-bug"}
