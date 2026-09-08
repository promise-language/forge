<!-- forge:dev-tooling -->
## Dev tooling

Dev tools are compiled from a single in-repo Go module (`tools/build/`) into
`bin/`, which is gitignored — the tools are always built locally, never committed.

**Fresh clone — bootstrap once:**

```bash
./make            # Windows: .\make.cmd
```

`./make` compiles every tool into `bin/` and wires up the git pre-commit hook.
It is idempotent and finishes in well under a second once built.

**Before every commit — run the gate:**

```bash
bin/verify        # format → vet → build → test, then a pass/FAIL summary
```

A green "OK to Commit" line means it is safe to commit; a red FAIL means it is
not. The pre-commit hook (`bin/precommit`) also blocks staged binaries, enforces
GitHub noreply commit identities, and keeps the tree source-only.

**Edit a tool, then rebuild.** If any tool prints
`tools source has changed — run: ./make`, re-run `./make` to rebuild `bin/`.
That is the whole loop: edit → `./make` → use. Add a tool by dropping a new dir
under `tools/build/cmd/` and re-running `./make` — no registration step.
<!-- /forge:dev-tooling -->
