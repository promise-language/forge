// Command run asks one gate for a measurement and reaches a verdict on it.
//
// This is the by-hand path, and it takes the same route a runner takes rather
// than a parallel one: it executes bin/gate as a process and reads what came
// back. Running a single gate is not a lesser case — it is faster than
// everything that blocks a change from landing, and it is what someone
// iterating on one failure actually wants.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/promise-language/forge/primitives"
	"github.com/promise-language/forge/tools/build/common"
)

// Injected by the meta-builder via -ldflags at build time; empty otherwise.
var (
	repoRoot   = ""
	sourceHash = ""
)

func usage() string {
	var sb strings.Builder
	sb.WriteString("run — measure one gate and judge what it measured.\n\n")
	sb.WriteString("Usage:\n  run <gate> [-help]\n  run <gate> --verdict < envelope\n  run --list [-json | -human]\n\n")
	sb.WriteString("Runs bin/gate <gate> --envelope, then prints each measurement beside the\n")
	sb.WriteString("term it was judged on. Exit 0 means every capped measurement is within its\n")
	sb.WriteString("cap; non-zero means one is not, or that nothing could be measured.\n\n")
	sb.WriteString("With --verdict it judges an envelope it is GIVEN, on stdin, and runs no\n")
	sb.WriteString("gate: it prints one JSON verdict on stdout and nothing else. That is the\n")
	sb.WriteString("mode the SDK asks — the SDK spawns the gate, because a judge that ran its\n")
	sb.WriteString("own measurement would be the runner, and the runner comes from outside the\n")
	sb.WriteString("tree.\n\n")
	sb.WriteString("Gates:\n")
	for _, n := range common.GateConcepts() {
		fmt.Fprintf(&sb, "  %-12s %s\n", n, common.GateSummary(n))
	}
	sb.WriteString("\nA name is a concept, optionally with an instance naming one module:\n")
	fmt.Fprintf(&sb, "  %s\n", strings.Join(common.ModuleLabels(repoRoot), ", "))
	sb.WriteString("Omitting the instance measures every module.\n")
	capped := common.CappedMetrics(repoRoot)
	if len(capped) > 0 {
		fmt.Fprintf(&sb, "\nJudged against a cap: %s\n", strings.Join(capped, ", "))
	} else {
		fmt.Fprintf(&sb, "\nThresholds defined in %s\n", common.ManifestFile)
	}
	sb.WriteString("Anything else is reported and not judged.\n\n")
	sb.WriteString("With --list it names what this project builds and what it answers:\n")
	sb.WriteString("`commands` are the binaries in bin/, `gates` are the names bin/gate\n")
	sb.WriteString("--list declares. That is the discovery query — it is how anything\n")
	sb.WriteString("outside the tree learns both without holding a copy that can go stale.\n")
	sb.WriteString("Output is human-readable at a terminal and JSON when stdout is not one;\n")
	sb.WriteString("-json and -human force the mode, and passing both is a usage error.\n")
	return sb.String()
}

// listAnswer is what --list prints: the two kinds of name a caller outside this
// tree can ask this project for — a command it can execute, and a gate it can
// measure. Both are discovered rather than declared, so neither can go stale,
// and one JSON object carries them together because a caller that must not
// confuse the two needs to see the whole vocabulary at once.
type listAnswer struct {
	Commands []string `json:"commands"`
	Gates    []string `json:"gates"`
}

// wantsList reports whether the argv names the discovery query, so main can
// route to it before ParseRunArgs refuses `-list` as an unknown gate flag.
func wantsList(args []string) bool {
	for _, a := range args {
		if a == "-list" {
			return true
		}
	}
	return false
}

// renderList writes the answer in the mode stdout asked for (cli-guide §6).
//
// The human form is one name per line with its kind, because the two lists are
// answers to different questions — what this project builds, and what it
// answers — and a reader who cannot tell which is which has to know the
// vocabulary already to use the output that exists to teach it.
func renderList(w io.Writer, answer listAnswer, mode common.OutputMode) error {
	if mode == common.OutputJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(answer)
	}
	for _, c := range answer.Commands {
		if _, err := fmt.Fprintf(w, "command  %s\n", c); err != nil {
			return err
		}
	}
	for _, g := range answer.Gates {
		if _, err := fmt.Fprintf(w, "gate     %s\n", g); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	args := primitives.NormalizeArgs(os.Args[1:])
	if primitives.HasHelpFlag(args) {
		fmt.Print(usage())
		os.Exit(0)
	}
	common.CheckStale(repoRoot, sourceHash)

	// The discovery query. It comes after CheckStale for the reason the verdict
	// mode does: a binary whose logic has moved since it was compiled must not
	// answer with names it may no longer implement. It comes before
	// ParseRunArgs because that refuses an unknown flag rather than ignoring
	// it, and --list is not a gate name.
	if wantsList(args) {
		// §8: every problem with the invocation is reported before anything is
		// done, and a malformed invocation exits 2 having written nothing.
		rest, outFlags := common.TakeOutputFlags(args)
		var bad []string
		for _, a := range rest {
			if a != "-list" {
				bad = append(bad, a)
			}
		}
		var problems []string
		if len(bad) > 0 {
			problems = append(problems, fmt.Sprintf("unknown flag(s) alongside --list: %s", strings.Join(bad, ", ")))
		}
		mode, err := outFlags.Mode()
		if err != nil {
			problems = append(problems, err.Error())
		}
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintf(os.Stderr, "run: %s\n", p)
			}
			fmt.Fprintf(os.Stderr, "run: see `%s -help` for the supported flags\n", os.Args[0])
			os.Exit(2)
		}

		commands, err := common.CommandNames(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run: cannot say what this project builds: %v\n", err)
			os.Exit(1)
		}
		answer := listAnswer{Commands: commands, Gates: common.GateNames(repoRoot)}
		if err := renderList(os.Stdout, answer, mode); err != nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
			os.Exit(1)
		}
		return
	}

	name, verdict, err := common.ParseRunArgs(repoRoot, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v; run `%s -help` for usage\n", err, os.Args[0])
		os.Exit(2)
	}

	// The judging mode. Nothing is spawned: the envelope arrives on stdin from
	// whoever ran the gate, and stdout carries one verdict and nothing else.
	// CheckStale has already run above, so stale tooling exits before it can
	// print a verdict rather than answering with terms nobody currently holds.
	if verdict {
		if err := common.JudgeStdin(repoRoot, name, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := common.RunOneGate(repoRoot, common.GateBinary(repoRoot), name); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
