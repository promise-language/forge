//go:build windows

package tooling

// A child that will not end on its own, so that what ends it in the test is the
// run and nothing else.
func sleeper() string     { return "cmd" }
func sleepArgs() []string { return []string{"/c", "timeout", "/t", "60", "/nobreak"} }
