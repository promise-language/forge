//go:build !windows

package tooling

// A child that will not end on its own, so that what ends it in the test is the
// run and nothing else.
func sleeper() string     { return "sleep" }
func sleepArgs() []string { return []string{"60"} }
