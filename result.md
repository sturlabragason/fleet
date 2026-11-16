# Test prune, phase 1: proposal (nothing deleted yet)

The suite has few low-value tests. Most tests name a bug or ADR and are kept. The real win is
runtime: about 25 tests spend their time in fixed sleeps or full timeouts. Deleting and merging
the low-value tests saves about 23 tests, 30 table cases and 650 lines (about 1.6% of the 40k
test lines), and with the speed-ups CI time drops by about 45 s of the 190 s summed test time.
By package: internal/fleet ~100 → ~78 s, cmd/fleet ~36 → ~17 s, internal/wall ~30 → ~22 s.
The race job (3:05) is the long pole, and it shrinks by the same share.

Timings come from one `go test -json ./...` run on GitHub Actions (throwaway branch, now
deleted), not from the laptop. Five fresh-context reviewers read every test file and checked
each overlap claim against the covering test's assertion.

## 1. Delete (23 tests, ~520 lines, ~4 s)

| pkg | test | why | still covered by |
|---|---|---|---|
| cmd/fleet | TestGraphAfterMerge (whole file) | restates `mode=="merge" && found` | – |
| cmd/fleet | TestWatchInUsage | usage contains a copied literal | parity_test TestEveryCommandHasAdmiralParity |
| cmd/fleet | TestPinMsg | re-types the two strings pinMsg returns | – |
| cmd/fleet | TestPreviewPopupArgs | literal argv copy | TestMenuArgs `all` block |
| cmd/fleet | TestUnbindsPaneDrags | loops over the 3 keys tmuxCommands emits | – |
| cmd/fleet | TestSupervisorAliasResolvesToAdmiral | | TestRunDispatch `supervisor ls` row |
| cmd/fleet | TestFailedSwapFreshRestore (0.84 s) | subset | TestSwapAdmiralEndToEnd "dies at once" phase |
| cmd/fakeagent | TestACPCrashExits3 (own `go build`) | tests the test double | acphost TestE2ECrashIsError, fake acp_test `[crash]` |
| internal/fleet | TestSpawnLsSendClose (0.81 s) | oldest smoke test, each step covered | TestMenuPaneAgreesWithBoard, TestACPDriver, TestResumeClosedAgent |
| internal/fleet | TestSpawnFakeAgentEndToEnd (0.56 s) | weaker copy of the ADR-13 guard | TestSpawnPromptWaitsForReady |
| internal/fleet | TestWatchPaths | tmux server to check one constant path | – |
| internal/fleet | TestNoExtensionsNoBadge | nil default | TestExtensionsCompose |
| internal/fleet | TestNonLeaderPromptHasNoSquadOrientation | exact-equality check catches it | TestSpawnPrependsPlaybook |
| internal/wall | TestPresetUp | asserts the presetUp map literal | TestAddStepsThePresetUp |
| internal/wall | TestFloorKeepsProtectedPanes (0.64 s) | same eviction path | TestFloorKeepsTheTargetPane |
| internal/mcp | TestResetShellUsesAdmiral | literal shell string | – |
| internal/mcp | TestInstructionsCarryTimeRule, ...ProcessRules | copied phrases from instructions.md, no ADR (your call: they also stop the rules being dropped by accident) | – |
| internal/board | TestDriverName | accessor with a default | – |
| internal/prefs | TestThemePref, TestAdmiralProfile | trivial accessors | – |
| internal/summary | TestDefaultArgsAreNonInteractive | pins DefaultArgs constants | – |
| internal/legacy | TestMoveDirNewAndLinkIsNoop | | TestMoveDirMovesOldAndLinks "second call" |

## 2. Drop table cases and asserts (~30 cases, ~60 lines)

Same branch, different literal: mousecopy "tmux defaults copy nothing" (a tmux control case,
**3.9 s**), TestRunDispatch help/-h/--help and arity dupes, TestCheckTmux 2, TestVersionLine 1,
TestTopLevelProfile 1, TestDriverResolution 1, TestDriverDefaultOverrides 4, TestPickModel 2,
TestAdoptStorelessWords 1, TestWakeUrgentSkipsSettle `blocked`, TestSlotsStepDownWithTheWindow 2,
TestEventState 2, TestNoReport 2, TestBlockStop 2, TestDefaultsAndArgv copilot-resume, TestFiles 1.
Copied-literal asserts trimmed (the behaviour checks stay): TestSupervisorMCPArgs copilot half (it also
reads the real ~/.copilot/mcp-config.json), TestWallCmdActQuotesExe, TestChangesBindings,
TestPickModel popup strings, TestStuckLine, TestSafeDirRedirects note, TestSpawnSchemaHasTheTaskCard
loop, TestRoundTrip adopt loop and a 300 ms sleep, TestWaitAdmiral 1 s timeout (TestWaitSendsProgress has it).

## 3. Merge shared slow setups (~70 lines, ~10 s)

One fixture, each old test kept as a phase or subtest:
- cmd/fleet: 7 one-pane `configure()` tmux tests (up_test, peek_live_test, changes_test); 4 spawn_test leader tests.
- internal/wall: undo (3 → 1), Show* (4 → 1), Apply* (4 → 1), HideAll/Restore (3 → 1), eviction into ShowRefuses.
- internal/mcp: TestFleetTeam + TestPeekSummaryTool + FailsOpen into TestSupervisorErgonomics.
- internal/fleet: park driver tests (3 → 1), WakeResumesInTheConfiguredMode into WakeKeepsContextLevel,
  AutoCloseReadOnlyAndNonGit into AutoCloseDone, squad peer-nudge into SquadReportWakesTheLeaderPane,
  StuckOnlyReportsWhenDraining into StuckReportsOnce; `buildFleet` builds once per package run (5 `go build`s → 1).
- internal/adr: the two git-repo tests.

Merges that would collide on asserts or global state (TestSquadClose, summaryFleet, wtFleet, wake settle) are left out.

## 4. Speed up, keep the test (~30 s)

| test | CI s | cause | fix | saves |
|---|---|---|---|---|
| TestMouseDragOverMouseReportingPaneCopies | 11.9 | fixed sleeps ×3 cases | poll; with the control case dropped | ~9.4 |
| TestCmdWatchDeliversQueuedWake | 5.8 | watcher checks "session gone" every 5 s (`watch.StopEvery`) | make it a var, 50 ms in the test | ~5.2 |
| TestWakeLandsInOwningAdmiralPane | 3.4 | waits the full 1.5 s GuardWait twice | `GuardWait = 20ms`, like wake_test | ~3.2 |
| TestSpawnLongPromptViaPaste | 3.1 | waits the full 3 s ready_timeout | ready_timeout 200ms | ~2.8 |
| TestRefreshNoReportWaitsForAStillScreen | 5.2 | fixed 5 s loop | NoReportAfter 1.5 s, loop 2.5 s | ~2.5 |
| TestNetworkOutage | 5.0 | fixed 3 s loop | poll for state, then 5 refreshes | ~2 |
| TestGuardReapsProcessesThatOutliveTheirAgent | 2.3 | full 2 s `treeGrace` const | make it a var, 100 ms in the test | ~2 |
| TestRestoreAndHideAllResizeEachPaneOnce (#41) | 3.2 | 4 × 600 ms sleeps | poll, keep 300 ms for the 2nd SIGWINCH | ~1.8 |
| internal/fake runFake + TUI fake | – | 6 × 300 ms sleeps | poll for the redraw | ~1.2 |
| TestRunWait | 2.0 | waits a real 1 s timeout | 100 ms | ~0.9 |
| smaller: ACPDriver, ACPStopWaitsForHostExit, stuck sleeps, HungCommandTimesOut, RefreshUserNoReport pane sleep | | | | ~1.5 |

`watch.StopEvery` and `treeGrace` go from const to var: a non-behavioural code change, so no
release unless you want one.

## 5. TestSpawnCodeGraph isolation bug

Cause: the test's tmux server inherits this process's `GRAPHIFY_OUT` (fleet sets it for its agents),
so the "no graph" spawn sees the real `~/.fleet/graphs/...`. Fix: `t.Setenv("GRAPHIFY_OUT", "")`.
The same leak exists in fleet itself: `start()` only sets GRAPHIFY_OUT when the repo has a graph, so a
fleet started from inside an agent hands the parent repo's graph to agents in graph-less repos.
Fixing that is one `else` line in internal/fleet/fleet.go (a patch release). I'd include it.

## Not proposed

- Status fixture tests, arch/parity guards, ext_example seam tests, testenv leak tests, and any test that
  names a bug or ADR, unless another test has the exact assertion.
- CI follow-up (outside the test files): the `extexample` job re-runs every test in 7 packages with
  the tag. `-run` on the example tests would save more runner minutes than any single row here.

## Open questions for Sturla

1. Approve sections 1–4 as listed? Default: yes, except the two TestInstructionsCarry* tests (kept).
2. Include the product GRAPHIFY_OUT fix and cut a patch release? Default: yes.
3. Take the extexample `-run` filter too? Default: no, separate change.

Branch: fleet/test-prune (no commits yet).

## Handoff state (for the next agent)

Status: phase 1 done and reported as `question`; waiting for Sturla's OK. No test is edited yet.

Not yet reviewed, or only partly (no candidates come from these):
- internal/acp, internal/acphost: only the sleeps and timings were checked, not each test body.
- internal/keys: test names only.
- internal/watch, update, whatsnew, sessions, recall, routes, usage, rules, testenv bodies;
  worktree (worktree_test, create, remote, stale, touching); proc (scan, gui, docs); the rest of trust and team.
- internal/tools: only 3 tests read. Candidates so far: TestShellCommand (restates the builder),
  TestClaudeTmuxArgvUnchanged (literal argv snapshot), TestExampleFile (merge into TestClaudeModelsAreNewest).
  Overlap not verified.
- Smaller extras found: status TestMenu idle/working loop (covered by TestPlainMenuNoFalsePositives);
  companion TestErrorMessageSeesWrappedCause (literal Error() text, moderate);
  proc TestKillEscalatesToSIGKILL 100 ms sleep → poll; worktree TestMergeSharedDocConflict final `git remote` assert.

Method that worked:
1. Per-test CI timings: push a throwaway branch `fleet/<x>-timing` whose ci.yml test step runs
   `go test -json ./... > t.json`, piped through jq to TSV, and uploaded as an artifact. Then
   `gh run download <id> -n timing`. Delete the branch afterwards.
2. Write one shared brief (prune categories A–F: literal restatement, constants/accessors, duplicate
   cases, verified overlap, shared slow setup, fixed sleeps; keep bug/ADR/guard/fixture tests unless an
   exact covering assertion exists). Then run 5 general-purpose subagents in parallel, one per package
   group (fleet acp..offline / fleet park..worktree / cmd / mcp+wall+ui+acp* / everything else).
   The explore-type subagents were too shallow; general-purpose ones read each file.
3. Copies of the brief and the timing TSV are in `.work/` in this worktree (gitignored, local only).

CI timing data (`go test -json` on ubuntu-latest, main at 1f612975). Summed per package:
internal/fleet 95.4 s/233 tests, cmd/fleet 34.8/133, internal/wall 29.7/58, internal/mcp 10.1/84,
worktree 6.6/34, acphost 3.4/52, testenv 3.3/4, fake 2.8/19, summary 2.1/17, tmux 1.4/13; the rest under 1 s.
Package wall time on main CI: fleet 102 s, wall 36 s, cmd/fleet 36 s, mcp 11 s. Jobs: test 2:33, race 3:05, extexample 2:17.
Slowest tests: MouseDragOverMouseReportingPaneCopies 11.9, CmdWatchDeliversQueuedWake 5.8, ACPEndToEnd 5.8,
RefreshNoReportWaitsForAStillScreen 5.2, NetworkOutage 5.0, WakeLandsInOwningAdmiralPane 3.4,
SpawnPromptWaitsForReady 3.3, RestoreAndHideAllResizeEachPaneOnce 3.2, SpawnLongPromptViaPaste 3.1,
testenv MainFailsAndKillsALeak 3.1, ClaudeACPEndToEnd 2.5, SwapAdmiralEndToEnd 2.35, GuardReaps 2.3,
SpawnModelEveryEffort 2.2, ACPDriver 2.2, RunWait 2.0, summary CLITimeoutSurvives 2.0, BinaryBumpHotApplies 2.0.

Phase 2 after the OK: small commits per section, the GRAPHIFY_OUT test fix (+ product fix if approved,
with a CHANGELOG line), make build && make lint && go vet on changed pkgs locally, push fleet/test-prune,
wait for green CI, fleet merge, push main, patch release only if non-test code changed.

## Sturla's decisions (2026-11-16 17:17)

1. Approved sections 1–4, keeping the two TestInstructionsCarry* tests.
2. Fix the product GRAPHIFY_OUT leak too, then cut a patch release.
3. extexample `-run` filter: yes, as a separate change.
A cluster agent implements phase 2 from this file.
