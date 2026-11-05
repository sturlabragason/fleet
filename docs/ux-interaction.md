# Mouse and keyboard on the overview and the wall

Status: **proposal, nothing here is built yet.** It answers "clicking around in fleet is not
intuitive" and "the overview breaks past 14 agents". The current behaviour (section 1) was read
from the code at `c1b9dfe` on 2026-11-05. tmux defaults were checked on tmux 3.8.

## 1. What each click and key does today

### Overview pane (fleet TUI, Bubble Tea)

| Input | What happens | Code |
|---|---|---|
| Left click on a card or a chip (on release) | Selects the card and runs Show (below) | `internal/ui/model.go` `Update` |
| `Enter` | Show for the selected card | same |
| Double click, right click, middle click, wheel | Nothing | no handler |
| Click on `+N hidden:` or `+k` | Nothing | no zone |
| `←↑↓→` `hjkl` | Moves the card cursor | `key` |
| `Tab` | Moves the cursor to the next waiting card. It does **not** show it | `key` |
| `x` | Takes the selected agent off the wall | `key` |
| `1 2 4 6 9 m` | Wall preset (slot cap) | `key` |
| `g` `R` `P` `?` `q` | Changes popup, reset admiral, profile picker, help, quit | `key` |

**Show** (`internal/wall/wall.go` `Show`):

1. Agent already on the wall: focus jumps to its pane. Nothing moves.
2. Wall **full** and the pane you were last in (`#{pane_last}`) is an unpinned wall pane: the
   agent swaps into that slot (§5b.1 "take the slot I just left").
3. Wall full, no last pane: the worst eviction class leaves (§5b.1), or nothing happens and
   tmux says `wall full: ...`.
4. Wall **not full**: the agent is appended as a new slot. Nothing is replaced.
5. Focus then moves into the new pane.

### Wall panes (tmux root key table)

| Input | What happens |
|---|---|
| Left click | `select-pane` plus the click is forwarded to the agent. The `pane-focus-in` hook re-tiles: the clicked pane becomes the emphasis pane and grows to 60% (§5b.3) when four or fewer panes are on the wall |
| Double/triple click | Select word/line into copy mode, or forwarded when the agent has mouse reporting on |
| Right click | tmux pane menu (Mark, Zoom, Kill...) only if the agent has mouse reporting **off**. Otherwise the agent gets the click and no menu appears |
| Drag | Copy-mode selection to the clipboard (ADR-28), or forwarded |
| Drag a border | Resize. The next re-layout snaps it back |
| Alt-drag a pane, Ctrl-drag | tmux 3.8 defaults `move-pane -M` and `new-pane -M`. fleet does not unbind them, so they can move a pane out of the wall order or create an untracked pane |
| `Alt-←↑↓→` `Alt-z` `Alt-x` `Alt-o` | Move, zoom, hide, jump to marked |
| `Alt-1..9/m`, `Alt-w` chord | Presets |

### Inconsistencies and dead ends

1. **The user's flow only works on a full wall.** Click pane B, click card X: on a full wall X
   replaces B. With a free slot (after `Alt-x`, or 3 panes under preset 4) X is appended and B
   stays. Same gesture, two outcomes, and nothing on screen says which one comes next.
2. **The target is invisible.** Once you click the overview, the overview owns the active
   border. Which wall pane is `#{pane_last}` is shown nowhere.
3. **Selecting a slot resizes it.** Clicking a pane to "choose" it fires emphasis, so the layout
   reflows before you have done anything. Every agent TUI in a resized pane repaints.
4. **Cards move under the mouse.** Cards are re-sorted by attention on every 1 s poll. A card
   that starts waiting jumps to the front. A click aimed at one card can land on another.
5. **Two unrelated "selected" marks in the same colour.** The card cursor (thick lavender
   border) and the active pane (lavender border) both look selected, but they are not linked.
   `▣` says "on the wall", not which pane. *(Fixed by ADR-37: lavender is the `▶` target
   only, the cursor is an underline in the status colour, the active pane is the bright neutral.)*
6. **`Tab` only moves the cursor.** It never brings the waiting agent onto the wall, so working
   through approvals is Tab, Enter, click the pane, answer, click the overview, Tab...
7. **No add-without-replace and no undo.** On a full wall, one click on the wrong card evicts a
   pane, and the only way back is to find its card and click again (which evicts another one).
8. **DESIGN.md §5 and §5a disagree with the registry.** §5 lists `x` reject, `Space` peek, `1-9`
   repo groups, `P` pin, `Z` zoom. The built keys are `x` hide, `1..9` presets, `P` profile
   picker, `Alt-z` zoom. A reader cannot tell which is real.
9. **Overflow agents are barely reachable** (section 4).

### What fleet can and cannot intercept

- tmux sees every mouse event first. fleet can rebind `MouseDown1Pane`, `MouseDown3Pane`,
  `DoubleClick1Pane` and so on, run tmux commands, and still forward the event with
  `send-keys -M`. Inline tmux commands (`set -wF @x '#{pane_id}'`) cost nothing. A
  `run-shell fleet ...` per click costs a process start (about 10 ms) and must stay off the
  plain-click path.
- fleet cannot tell what a click meant **inside** copilot or claude. A click on a wall pane is
  always also a click for the agent. So a click on a pane can only mean "this pane", never
  "do X to this pane", unless a mode is armed first.
- Keys typed in a wall pane go to the agent. Only root-table chords (`Alt-...`) reach fleet.
  Plain keys work only while the overview pane has focus.
- **Shift+click never reaches tmux** in VTE (Ptyxis, GNOME Terminal), kitty, WezTerm or foot:
  the terminal keeps it to bypass mouse reporting for text selection. Ctrl+click opens links in
  several of them. Neither can carry a fleet action.
- **Shift+Enter is not visible** to fleet's Bubble Tea v1.3.10: it arrives as `enter`. It needs
  Bubble Tea v2 and the kitty keyboard protocol.
- Drag a card onto a pane: the drag belongs to the overview pane. tmux does not deliver the
  release to the pane under the cursor. Not feasible.
- Hover: the overview can see motion over itself (`WithMouseAllMotion`), not over wall panes.

## 2. Familiar models

| Tool | Model | What carries over |
|---|---|---|
| tmux | Active pane is the target. `swap-pane`, `join-pane`, `choose-tree` picks a source | Target = focused pane. Fleet already uses this, but loses it when focus moves to the overview |
| i3 / sway | Focused container is the target. New windows open next to it. Marks name a container | Focus is the target, and focus survives switching. "Split here" vs "replace" are different keys |
| VS Code editor groups | Active group gets the opened file. Preview tabs (italic) are replaced by the next single click, a double click keeps it | Single click = replace the target. A separate gesture adds or keeps |
| Browser tabs | Click = switch, middle/Ctrl click = new tab | "Open in a new slot" is a modifier gesture, and the target never moves |
| Zellij / wezterm / kitty | Focused pane is the target. Modes (pane mode, resize mode) shown in a status bar | A permanent hint line that names the current mode and target |
| Grafana / OBS scenes | Select the target slot, then pick the source. The slot stays selected | Exactly the user's request. The target is drawn highlighted until you pick another |

Common ground: **one visible target that stays put**, a plain click replaces, a distinct gesture
adds, and a status line names the target.

## 3. Interaction models

Common to all three: the admiral is never a target, a pinned pane is never replaced, and a
replace keeps the slot's position and size (`swap-pane`, as today).

### Model A: sticky target slot (recommended)

The wall has one **target slot**, stored as window option `@fleet_target` (a pane id). It is set
by clicking a wall pane or moving to one with `Alt-←↑↓→`. It is **not** cleared when focus
moves to the overview or the admiral. A card click puts that agent into the target slot, full
wall or not.

| Input | Where | Action |
|---|---|---|
| Click a wall pane | wall | Focus it and make it the target. The click is forwarded to the agent as today. **No re-tile** |
| Click a card | overview | Put that agent into the target slot. The target stays on that slot, so clicking cards one after another cycles agents through the same slot |
| Click a card of an agent already on the wall | overview | Make its pane the target and focus it. Nothing moves |
| Right click a card | overview | Small menu: Replace target, Add as a new pane, Preview, Take off the wall, Pin |
| `Enter` | overview | Same as a click on the cursor card |
| `a` (also `+`) | overview | Add the cursor card as a new slot. The wall grows by one (section "Full wall") |
| `Tab` / `Shift+Tab` | overview | Put the next/previous waiting agent (longest wait first) into the target slot |
| `Alt-n` | anywhere | Same as `Tab`, from inside a wall pane, so you can answer, press `Alt-n`, answer the next |
| `Space` | overview | Preview the cursor card in a full-size popup. `Enter` in the popup replaces the target, `Esc` closes |
| `u` | overview | Undo the last replace: the previous agent goes back into that slot |
| `Esc` | overview | Clear the target. Clicks then fall back to today's rules (append, else evict by class) |
| `x` / `Alt-x` | overview / wall | Take the cursor agent / focused pane off the wall, as today |

**How it shows**

- The target pane's border title gets `▶` and the select colour, and keeps it while the overview
  has focus. tmux `pane-border-format` reads `#{==:#{pane_id},#{@fleet_target}}`, so no process
  runs.
- The target's card gets the same `▶` and colour. The keyboard cursor becomes a separate,
  quieter mark (underlined name; ADR-37 kept the thick border as a second, colour-blind-safe
  cue), so "where the next card goes" and "which card the keys act on" no longer look alike.
- Linked highlight: the cursor card's wall pane, if it has one, gets a dim marker in its title
  (`@fleet_hl`). With all-motion mouse tracking, hovering a card does the same.
- The overview header names the state: `▶ slot 2: orch-auth · click a card to replace ·
  right-click: add · Tab: next waiting · u: undo`. With no target:
  `no target: click a wall pane, or a card adds to the wall`.

**Undo and escape.** `u` reverts the last replace (stored as slot pane id plus the agent it
held). One level. `Esc` drops the target. Hiding the target pane (`x`, `Alt-x`) also drops it.

**Full wall.** Replace never needs a free slot. Add on a full wall steps the preset up
(4 → 6 → 9) because you asked for one more pane. At 9 it refuses with a message. With no
target, a click on a full wall evicts by class as today, and the pane that would leave carries
a dim `next out` mark so the outcome is visible before the click.

**Edge cases**

- Waiting or failing target: replacing it is allowed, because an explicit choice beats class-1
  protection (as §5b.1 already says). The agent keeps running off the wall and its card stays in
  the attention row, and `u` brings it back.
- Zoom (`Alt-z`): the zoomed pane is the target. The overview is hidden while zoomed, so card
  clicks are impossible. `Tab` from the overview or `Alt-n` unzooms, replaces, and re-zooms.
- Emphasis: clicking a pane no longer gives it 60%. Emphasis follows attention only (waiting,
  error, stuck). `Alt-z` is the "give me space" gesture. **This changes ADR-16** (open question 1).
- Teams: a team header or chip click is not a target action in this slice. Right-click on a team
  chip could offer "show team on the wall" later.
- A target pane that dies, is closed or auto-closed: the target clears and the header says so.

**Cost.** Two window options, a `MouseDown1Pane` rebinding (inline `set -wF`, then the default
`select-pane` and `send-keys -M`), `Show` gaining a target parameter, an overview header line, a
popup command for preview, a pane-border-format change, and an undo record. No new
dependencies.

### Model B: pick the agent, then the slot

Click a card to **arm** it (the card pulses, the wall dims), then click a wall pane to put the
agent there. `Esc` or a second click on the card disarms. Matches OBS "source then slot".

- Works with an empty or full wall in the same way, and the slot is explicit at the moment of
  the click.
- Needs a mode: while armed, the click on the pane must **not** reach the agent, so
  `MouseDown1Pane` becomes `if -F '#{@fleet_armed}' { ... } { select-pane; send-keys -M }`.
  A forgotten armed state eats a click meant for typing.
- Reverses the order the user asked for. "Select a terminal, then click a card" would arm
  nothing and do nothing.

Rejected as the main model. Its strength (no guessing which slot) is kept in A by making the
target always visible.

### Model C: tabs with a visible "next out"

Keep today's rules (append if room, else take the last pane, else evict by class) and only make
them visible: a `next out` mark on the pane that a card click would replace, linked
card/pane highlights, `Tab` shows the waiting agent instead of moving the cursor.

- Smallest change, no new state.
- Does not support "select slot, then click card" when the wall has a free slot, and the target
  still changes when focus moves to the admiral. The user's complaint stays half fixed.

Rejected. Its `next out` mark is kept in A for the no-target case.

### The five drafted ideas (fleet-llm-sessions)

| # | Idea | Verdict |
|---|---|---|
| 1 | Always show where you are | **In A**: `▶` on the target pane and card, and the header line names the target and the next action |
| 2 | Focusing a pane highlights its card, and the reverse | **In A**: target is linked both ways. Cursor card highlights its pane (`@fleet_hl`). Hover is optional |
| 3 | Shift+Enter adds a pane without replacing | **In A, other keys**: Shift+Enter cannot be seen (Bubble Tea v1) and Shift+click never reaches tmux. Add is `a`/`+` and right-click → Add. Shift+Enter becomes an alias once fleet moves to Bubble Tea v2 |
| 4 | Tab sends the next waiting agent into your last slot | **In A**: `Tab` in the overview and `Alt-n` anywhere replace the **target** slot (not `#{pane_last}`, which the admiral or the overview can overwrite) |
| 5 | Space previews an agent in a full-size popup | **In A, read-only**: `display-popup` running `fleet peek --live NAME` (capture-pane every 250 ms). An interactive popup would need a nested client on the same server, which tmux discourages and which breaks if the pane is on the wall. `Enter` in the popup = replace target |

Also: unbind tmux `M-MouseDrag1Pane`, `M-MouseDrag1Border` and `C-MouseDrag1Pane` in `fleet up`,
so a stray drag cannot pull a pane out of the wall order.

## 4. The overview past 14 agents

Today the strip is a fixed 14 rows (`OverviewRows`): a header and two rows of 6-row cards. On
the reference 209-column wall that holds 14 to 16 cards. The rest go into one `+N hidden:`
chip line, where chips that don't fit become an unclickable `+k`. The order re-sorts every
second.

| Option | For | Against |
|---|---|---|
| Compact one-line rows past N | 3 to 4 columns of 50-column rows give 33 to 44 agents in the same strip | Loses context meter and subagent row for everyone, including the agents that need you |
| Group or collapse by team | Fits how people think about related agents | Only helps when teams are used. Collapsed teams hide a waiting member unless the header shows it |
| Scroll with a `+N more` line | No information lost per card | What you can't see you can't click. Wheel in a 13-row strip is fiddly |
| Sink or hide done and idle agents | Most of a big fleet is idle. Fixes the count at the source | Hidden agents need a way back (a chip) |
| Grow the strip against the wall | More room exactly when needed | Every threshold crossing resizes every wall pane, and every agent TUI repaints. Fights §5b |

**Recommended: two tiers in a fixed-height strip, sorted stably.**

1. **Full cards** for agents that need you or are on the wall: waiting, error, stuck, the
   target, and the agents on the wall. This is at most about 9 + a few, and fits the first
   card row or two.
2. **One-line rows** for everyone else, in columns: glyph, name, team mark, status age. Working
   agents first, then idle, then done. One click works the same as on a card.
3. If even the one-line rows overflow, idle and done agents collapse into one clickable
   `12 idle ▸` chip that expands in place, and the wheel scrolls the one-line area. The last
   resort is scrolling, never an unclickable `+k`.
4. **Stable order.** An agent keeps its position within its tier. Tiers change on status
   change, but the overview defers re-sorting while the mouse is over it or a key was pressed in
   the last 3 seconds, so nothing moves under the pointer.
5. Within each tier, team members sit together with their team colour. Collapsing a team is
   later work.
6. The strip height stays fixed (wall geometry is worth more than overview rows). `Alt-c` keeps
   toggling chips-only. A third, taller size is an open question.

With 14 rows and 209 columns this shows full cards for up to 14 attention or on-wall agents and
one-line rows for about 30 more (three 60-column columns × 10 rows), before collapse.

## 5. Open questions (suggested default in brackets)

1. Should clicking a wall pane stop giving it emphasis (ADR-16 changes to "attention only",
   `Alt-z` for space)? [yes]
2. Add on a full wall: step the preset up (4 → 6 → 9), or refuse? [step up]
3. `Alt-n` as the global "next waiting into the target" chord? [yes; `Alt-Tab` belongs to the
   window manager]
4. Preview: read-only live popup now, interactive later? [read-only]
5. Overview scaling: two tiers plus stable order as above, or one-line rows for everyone past
   14? [two tiers]
6. Build order: A's target slot, header line and `Tab` first, then right-click menu, preview and
   undo, then the overview tiers. [this order, three commits]
