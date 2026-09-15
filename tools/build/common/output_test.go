package common

import "testing"

// §6: -json and -human force the mode, and passing both is a usage error rather
// than a precedence puzzle — the caller is told which two flags disagree, and
// nothing is rendered.
func TestModeRefusesBothFlagsByName(t *testing.T) {
	_, err := OutputFlags{JSON: true, Human: true}.Mode()
	if err == nil {
		t.Fatal("-json -human was accepted; §6 makes it a usage error")
	}
	for _, want := range []string{"-json", "-human"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestModeHonoursEachFlag(t *testing.T) {
	if m, err := (OutputFlags{JSON: true}).Mode(); err != nil || m != OutputJSON {
		t.Errorf("-json gave (%v, %v), want OutputJSON", m, err)
	}
	if m, err := (OutputFlags{Human: true}).Mode(); err != nil || m != OutputHuman {
		t.Errorf("-human gave (%v, %v), want OutputHuman", m, err)
	}
}

// Stripping is position-independent, so `run --list -json` and `run -json
// --list` are one invocation rather than two spellings (§4).
func TestTakeOutputFlagsStripsFromAnyPosition(t *testing.T) {
	rest, of := TakeOutputFlags([]string{"-json", "-list"})
	if !of.JSON || len(rest) != 1 || rest[0] != "-list" {
		t.Errorf("TakeOutputFlags = (%q, %+v), want ([-list], JSON)", rest, of)
	}
	rest, of = TakeOutputFlags([]string{"-list", "-human"})
	if !of.Human || len(rest) != 1 || rest[0] != "-list" {
		t.Errorf("TakeOutputFlags = (%q, %+v), want ([-list], Human)", rest, of)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
