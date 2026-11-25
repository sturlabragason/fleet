# Style guide

How fleet's docs, screenshots and colours should look, so that a reader who arrives from a
README, an issue or a screenshot always meets the same voice and the same palette.

The look is closer to a good engineering report than to a product landing page: white space,
plain words, real numbers, one accent at a time. It follows the maker's house brand (Untangle:
calm, exact, square corners, no depth), adapted to GitHub Markdown and a dark terminal.

## 1. Voice and tone

Write for someone running twenty agent sessions who has eleven seconds for your paragraph.

| Do | Don't |
|---|---|
| Short sentences, active voice, present tense | Stacked clauses and passive constructions |
| Specific numbers: "270 MB per agent", "polled every second" | "lightweight", "blazing fast", "scales" |
| Say what is built and what is planned, in the same breath | Describe planned work as if it exists |
| Talk to the reader: "your agents", "you decide" | Label the audience: "for power users", "enterprise teams" |
| Name the trade-off and the reason | "simply", "just", "obviously" |
| Plain nouns for fleet's own parts: agent, board, wall, tile, pane | Invented capitalised product nouns |

Rules that have no exceptions:

- **No em dashes.** Use a comma, a colon, a full stop or parentheses. This matches the house
  brand and keeps diffs free of a character that is hard to type consistently.
- **Sentence case for every heading.** No title case, no capitals, no emoji, no trailing
  punctuation.
- **Mark unbuilt things.** "Planned", with a pointer to the section of DESIGN.md that specifies it.
  A reader must never have to run the binary to find out whether a feature is real.
- **No private data.** No client or employer names, no home paths, no email addresses, no
  hostnames, no tokens. `scripts/check-public.sh` enforces this on the way out; write as if it
  did not.
- **One claim, one source.** A performance or comparison claim says when it was measured or
  checked, on what, and what was not measured.

Danish is never used in the docs. The project's language is English.

### The naval analogy

fleet, admiral, wall: the analogy is useful exactly as far as it is built, and no further. Use it
for things a reader meets in the product, not as decoration in prose. The pane that steers the
fleet is the admiral (renamed from the supervisor by ADR-26), so write admiral: the old name
survives only in deprecated aliases (`fleet supervisor`, `FLEET_SUPERVISOR_PROFILE`), kept code
identifiers and the `supervisor/` compatibility symlink, and belongs in new prose only when the
sentence is about that compatibility. Beyond those three words, don't invent "flagship", "convoy",
"shipyard" or "harbour" for existing concepts, and don't rename anything to fit the metaphor. A
nautical word that is not a command, a pane or a file does not belong in the docs.

Tagline candidates, all in the house voice (short, concrete, about the reader's attention):

- **See and steer many coding-agent sessions from one tmux window.** The current line. Accurate,
  slightly long, works as a repo description.
- **One window. The whole fleet.** For the README header under the logo, a social card or a talk
  slide. Short enough to sit beside the mark.
- **Agents your attention can keep up with.** Closest to the house headline ("Platforms and AI
  your team can run"): it names the real constraint, which is attention, not terminals.

Pick one per surface. Don't print two of them on the same page.

## 2. Document structure

Every document under `docs/` follows the same shape:

1. `# Title` in sentence case, naming the thing, not the document type.
2. One or two sentences saying what the reader will be able to do, or decide, after reading.
3. For anything measured, surveyed or dated: a line saying when, on what, and how it was checked.
4. The content, heaviest-used section first.
5. Links out, at the end of the section that needs them, not in a trailing "see also" dump.

| Document | Answers |
|---|---|
| `README.md` | What is this, why would I run it, how do I try it without spending tokens |
| `DESIGN.md` | What is required, how it is built, why these decisions (ADRs) |
| `AGENTS.md` | What may an agent working on this repo do and not do |
| `docs/style.md` | How should docs, screenshots and colours look |
| `docs/perf.md` | What does a fleet cost in RAM and CPU, measured |
| `docs/prior-art.md` | What exists already, what should fleet borrow and avoid |
| `docs/process.md` | How does fleet run agent work, and why the playbook says what it says |

A new document earns its place by answering a question none of the others answer. Otherwise it is
a section in an existing one.

### Headings

- Sentence case, no trailing punctuation, no emoji.
- `##` for sections, `###` for the parts of a section. `####` means the section should be split.
- Numbered sections only where the document is referenced by number from elsewhere (DESIGN.md).
- A heading names a thing or a question, not an activity: "Status colours", not "Choosing colours".

### Tables

Tables are the default for anything with more than two parallel facts: commands, hotkeys, tools,
measurements, comparisons. They are the house's "work table": what it is, the figure, the result.

- Left-align everything. No alignment colons, no centring.
- The first column is the key the reader scans for, and is stable between documents (a command is
  always written `fleet ls`, an env var always `FLEET_HOME`).
- Header words are sentence case and short enough not to wrap: "What it does", not "Description of
  the behaviour of this command".
- Figures go in their own column, with the unit, and are written the same way down the column
  ("330-460 MB", not "about 400mb").
- A table with one row is a sentence. Write the sentence.

### Code, keys and paths

- Fenced blocks are `sh`, `go`, `toml` or `json`. Never unlabelled.
- Shell examples are copy-pasteable and never contain a home path. Use `~/.fleet/...`, `$FLEET_HOME`
  or a relative path.
- Hotkeys in backticks exactly as the TUI prints them: `Alt-?`, `Tab`, `g`.
- Inline code for commands, flags, files, env vars, statuses (`working`, `waiting`, `idle`) and
  tool names (`fleet_status`). Not for emphasis.
- Bold for the one word in a paragraph that carries it. At most one per paragraph. No italics for
  emphasis, no ALL CAPS.
- Blockquotes only for a warning the reader must not miss, like the autopilot blast radius note in
  the README.

## 3. Logo and marks

fleet has its own mark, and it stays fleet's. The maker's mark is separate and smaller, and the two
are never combined into one lockup or placed side by side at the same size.

| Mark | File | Use |
|---|---|---|
| fleet lockup | `docs/img/lockup.svg`, `docs/img/lockup-on-ink.svg` | README header, social card, talk slides |
| fleet icon | `docs/img/logo.svg`, `docs/img/logo-on-ink.svg` | Avatars, favicons, anywhere under 120 px wide |
| Maker's mark | `docs/img/sturla-bragason.svg`, `docs/img/sturla-bragason-on-ink.svg` | Once, at the foot of the README |

Both marks are framed with square corners, black on white, with a reversed version for dark
grounds (fleet's frame is a square, the maker's mark 1.88:1). Regenerate
fleet's own with `python3 scripts/build_logo.py <IBMPlexMono-Bold.ttf> docs/img`; never retype or
redraw either one, and never recolour, stretch, rotate, outline or shadow them. The one exception
is the terminal mark below, which is generated from the same geometry, not redrawn by hand.

### In the terminal

A terminal can't show the SVG, so fleet draws the mark in text (ADR-166). It is generated, never
typed: `python3 scripts/build_ascii_logo.py <IBMPlexMono-Bold.ttf> > internal/logo/art.go` routes
the SVG's five strands on a character grid and rasterises the word, and `internal/logo`'s golden
files pin the result. Three sizes, each used where it adds identity and never on a working screen:

| Size | Looks like | Where |
|---|---|---|
| Line, 1 row | `╳━■ fleet` | tmux status line badge, web mirror header |
| Mark, 3 rows | strands crossing into lanes, `fleet` beside it | Head of the help (`?` and `Alt-?`), only when it costs no key a row |
| Splash, 16 rows by 73 columns | the framed mark, `fleet` in half blocks | The overview with no agents |

- Colour comes from theme roles at render time: strands, frame and word in `theme.Text`, the
  blue lane and its stop in `theme.Select`. The status badge is the logo's primary form, black
  on white (`theme.Crust` on `theme.Supervisor`), with no accent.
- The blue lane is heavy (`━┃┏`) and its stop `■` is larger than the others' `▪`, as in the SVG.
- `TERM=dumb`, or a locale that is not UTF-8 outside tmux, gets the ASCII form (`X=# fleet`).
  Inside tmux fleet forces UTF-8, so the Unicode form always draws there.
- Never edit `art.go` by hand, never add a fourth size, and never put the terminal mark on a card,
  a tile or anywhere an agent's state is shown.

### Light and dark on GitHub

GitHub renders README images against the reader's own theme, so every mark is placed with a
`<picture>` element and the reversed file as the dark source:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/lockup-on-ink.svg">
  <img src="docs/img/lockup.svg" height="64" alt="fleet">
</picture>
```

The same pattern carries the maker's mark:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/sturla-bragason-on-ink.svg">
  <img src="docs/img/sturla-bragason.svg" height="64" alt="Sturla Bragason">
</picture>
```

- Always set `height`, never `width`: both marks keep their own aspect ratio.
- The maker's mark is 1.88:1, so `height="64"` renders it 120 px wide, which is its minimum size.
  Never smaller, and never larger than the fleet lockup above it.
- `alt` text is the name only. The mark is not a sentence.
- Clear space around either mark is a tenth of its width, which in Markdown means a blank line
  above and below, with nothing else on those lines.
- Never put a mark inside a table, a heading, a badge row or on top of a screenshot.

## 4. Colour

Two palettes, used for different things, and they do not mix.

**Documents** (README, docs, diagrams) use the house tokens: ink `#161616` for text and rules, grey
`#525252` for secondary text, white or `#F4F4F4` grounds, and Lane blue `#0033CC` for the single
accent in a figure. One accent per view. No gradients, no shadows, no rounded corners, no second
hue.

GitHub renders the same Markdown on a white page for one reader and a near-black one for the next,
so nothing in a document may assume its ground. Never set a text or line colour in Markdown, HTML
or Mermaid and leave the ground to chance: let GitHub's theme supply both, or supply both yourself
(an SVG with a light and a dark file behind a `<picture>`, as the marks do). Lane blue is not an
exception. `#0033CC` is 8.9:1 on white but 2.1:1 on GitHub's dark ground, so it may only be used
where the ground travels with it, and its reversed counterpart is `#8AA4FF` (7.6:1 on ink). The
ink and grey tokens are likewise for the printed and generated artefacts (the marks, a PDF, a
slide), where the ground is known.

**The TUI** uses the `lane` palette (`internal/theme`) by default (ADR-166), and the docs and
screenshots use it too. It is the house palette reversed for a dark terminal: ground ink
`#161616`, text `#f4f4f4`, the IBM Carbon grey ramp the brand greys come from, and the one accent
as the reversed Lane `#8AA4FF`. Lane blue itself, `#0033CC`, is 1.8:1 on ink and unreadable in a
terminal. The brand rule that carries over is the discipline: colour means status, never
decoration, and at most one thing per screen is allowed to be the accent.

The brand has one accent and no other hue, but a terminal of agents needs status colours. Those
come from Carbon's dark-theme support colours, the same family as the greys, each lifted to clear
7:1 on ink: green `#42be65`, yellow `#f1c21b`, and for errors the brand's Signal red lifted from
`#C40000` (2.9:1 on ink) to `#ff7a7a` (7.2:1), same hue.

The mapping from the house roles to the terminal palette:

| House role | Document token | TUI constant | Terminal colour |
|---|---|---|---|
| Accent, the one thing that matters | Lane `#0033CC` | `theme.Select` | Lane on ink `#8aa4ff` |
| Body text | Ink `#161616` | `theme.Text` | Paper grey `#f4f4f4` |
| Secondary text, resting state | Grey `#525252` | `theme.Idle` | Grey on ink `#8d8d8d` |
| Inactive, finished with | Hair `#E0E0E0` | `theme.Gone` | Grey `#525252` |
| Signal, something is wrong | Signal `#C40000` | `theme.Error` | Signal on ink `#ff7a7a` |
| Ground | Paper `#FFFFFF` | `theme.Base` | Ink `#161616` |

Status colours carry meaning, and that meaning is fixed:

| Status | Constant | Colour | Means |
|---|---|---|---|
| `working` | `theme.Working` | Green `#42be65` | Running, needs nothing from you |
| `waiting` | `theme.Waiting` / `WaitingAlt` | Yellow `#f1c21b`, pulsing to Orange `#ff832b` | Wants an answer from you |
| `error` | `theme.Error` | Red `#ff7a7a` | Failed |
| `stuck` | `theme.Stuck` | Pale red `#ffb0b0` | Grinding: the same error twice, or no output for a long time |
| `idle` | `theme.Idle` | Grey `#8d8d8d` | Alive, nothing to do |
| `gone` | `theme.Gone` | Grey `#525252` | Process ended |
| `queued` | `theme.Gone` | Grey `#525252` | Waiting for a slot, no pane yet |
| `starting` | `theme.Working` | Green `#42be65` | Launching |

Selection, focus and the target slot are three different things, so they look different (ADR-37).
The accent is spent on exactly one of them (the logo's blue lane is the only other use, ADR-166):

| State | Looks like | Constant |
|---|---|---|
| `▶` target slot (the next card lands here) | Bold `▶` before the pane title and after the card name | `theme.Select` `#8aa4ff` |
| Overview cursor (what `⏎` would act on) | An underline, in the cell's own status colour, plus a thick card border | the status colour, or `theme.Text` when that colour recedes (`gone`, `queued`) |
| Active tmux pane (where your keys go) | Heavy border, full-brightness text | `theme.Text` `#f4f4f4` |
| Any other pane | One step dimmer, same ground | Overlay2 `#a8a8a8` |
| Pinned `⚑`, `+N hidden`, `+N idle`, one-line row ages | Resting grey | `theme.Muted` / `theme.Idle` Overlay1 `#8d8d8d` |
| `· next out` (the pane a card would evict) | Dimmest, it is on its way out | `theme.Gone` `#525252` |

Dimming unfocused panes and the emphasis pane (ADR-16, off by default, `fleet wall emphasis`) must not
stack into "big but unreadable": emphasis only changes a pane's size, dimming only its text, and
Overlay2 stays at 7.6:1 on the base (lane) so an emphasised pane you are not typing in is still
legible. Do not take the dim step down to Overlay1 (5.5:1), which is the resting colour of things you are
meant to ignore. A card-to-pane highlight (`@fleet_hl` in docs/ux-interaction.md) is not built; when
it is, it is Overlay1, not a second accent.

### Themes

A palette is a set of *roles*, not of shades (ADR-55). `theme.Working`, `theme.Waiting`,
`theme.Select`, the grounds `Base`/`Mantle`/`Crust`/`Surface0-2` and the inks
`Text`/`Subtext0`/`Overlay2-0` mean the same thing in every theme; only the hex changes. There is
no `theme.Yellow` any more, because yellow means nothing on a grey theme.

Six hand-tuned palettes ship, plus 20 taken from the most-installed VS Code themes. Set one in `~/.config/fleet/config.toml` and run `fleet up` again: the tmux
chrome re-colours and the overview pane restarts, agents keep running.

```toml
[defaults]
theme = "stealth"   # lane (default), catppuccin, stealth, neon-drive, phosphor, graphite
                    # or a VS Code theme: shades-of-purple, dracula, github-light, ...
```

The VS Code themes are `github-dark`, `github-light`, `one-dark-pro`, `dracula`, `atom-one-dark`,
`ayu`, `winter-is-coming`, `night-owl`, `tokyo-night`, `one-monokai`, `palenight`, `synthwave-84`,
`shades-of-purple`, `cobalt2`, `andromeda`, `atom-one-light`, `noctis`, `nord`, `gruvbox` and
`material` (Catppuccin, also in the top 20, is `catppuccin` above). One function, `fromVSCode`, maps
each theme's published colours onto the roles and lifts a colour that misses its floor only as far
as the floor (ADR-174). Sources, licences and the themes left out are in
[THEMES.md](../THEMES.md).

| Theme | Ground | Working | Waiting | Error | `▶` Select | In one line |
|---|---|---|---|---|---|---|
| `lane` | Ink `#161616` | Green `#42be65` | Yellow `#f1c21b` | Red `#ff7a7a` | Lane `#8aa4ff` | The default: the house brand, greys and one blue |
| `catppuccin` | `#1e1e2e` | Green `#a6e3a1` | Yellow `#f9e2af` | Red `#f38ba8` | Lavender `#b4befe` | Mocha, the default until ADR-166 |
| `stealth` | `#0a0b0d` | `#53d6a0` | `#ffcf4d` | `#ff4d5e` | Ember `#ff7a18` | Near-black and quiet until something needs you |
| `neon-drive` | `#0b0f2a` | Cyan `#00e5ff` | `#ffd166` | `#ff3864` | Magenta `#ff2bd1` | A night-drive dashboard; the loudest |
| `phosphor` | `#06110a` | `#33ff77` | Amber `#ffc044` | `#ff3b2f` | White `#ffffff` | Green CRT: attention is the only thing that is not green |
| `graphite` | `#0d0d0f` | Grey `#a8a8b0` | White `#ffffff` | `#ff2200` | Amber `#ffa000` | Greys only: white means answer me, red means it died |

Every theme keeps the rules below, and `internal/theme`'s palette test enforces the measurable
half of them: each role clears its floor on its own theme's base, `gone` stays dimmer than `idle`
and `idle` dimmer than `text`, the unfocused-pane step (`Overlay2`) sits exactly between `Overlay1`
and `Text`, and nothing else may equal `theme.Select`. Add a palette and the test covers it.

Team colours come from the active palette (`theme.TeamPalette`, `theme.TeamHex`). Phosphor and
Graphite cannot offer six distinguishable hues, so their six teams are steps on the theme's own
ramp, told apart by lightness and by the team name — a deliberate cost, written down in ADR-55.

Rules for anyone changing `internal/theme`:

- A new colour is a new meaning. If it does not change what the user should do, it is decoration:
  use an existing role instead.
- Green and red are never the only difference between two states a colour-blind reader must tell
  apart. Every tile carries its status word and a distinct glyph (`working` a spinner, `waiting`
  `●`, `error` `✖`, `stuck` `⧗`, `idle` `○`, `gone` `·`, `queued` `‖`, `starting` `▸`), tiles are
  ordered by attention, and `waiting` pulses. Colour is the fastest signal, never the only one: a
  new status needs its own glyph before it needs its own colour.
- Lightness carries the hierarchy, so it survives any colour vision. Against the lane base
  `#161616`, the four statuses with a live process (`working`, `waiting`, `error`, `stuck`) sit
  between 7.2:1 (`error`) and 10.7:1 (`waiting`), while `idle` drops to 5.5:1 and `gone` to 2.3:1
  on purpose: they recede. Keep a new
  status above 7:1 if it should be noticed, and don't lift `idle` or `gone`.
- The dim step of the running-subagent wave (`theme.WorkingDim`, `#198038` in lane, 3.6:1) stays a darker
  green, never grey, so it cannot be read as a finished dot.
- Team colours come from `theme.TeamPalette` and must stay out of the status range: no green,
  yellow, peach, red or maroon, and not the selection colour. Lane's six teams are cool hues
  (purple, teal, cyan, lilac, aqua, slate): a documented cost against the brand's single accent,
  because a team needs a hue. A new palette fills every role,
  including its own six team colours; a role may never be left empty.

## 5. Screenshots and diagrams

Screenshots:

- Made with fake agents (`scripts/screenshot.sh`), never with a real session. A real pane leaks
  repository names, paths and prompts, and `check-public.sh` cannot see inside a PNG.
- Composed, not waited for: the script holds each agent in a chosen state with the fake agent's
  `!work`, `!wait`, `!error` and `!done` directives (ADR-52), so the wall always shows one agent
  working, one waiting on you, one failed and one done. Regenerating after a palette or layout
  change gives the same composition, not a new roll of the dice.
- Agent names come from the generic set used elsewhere in the docs (`api-auth`, `web-nav`,
  `infra-dns`). No client or employer words, ever.
- One window, full fleet layout, no desktop around it, no window shadow, no rounded corners, no
  device frame, no cursor. The padding around the capture is the terminal's own ground, so the
  image has no edge of its own.
- 200 columns by 40 rows at 15 px (`SHOT_COLS`, `SHOT_ROWS`, `SHOT_FONT`), which is the widest
  the tmux status line stays untruncated and the shortest that leaves no dead band under the
  panes. The gif renders on the same pinned grid, so every frame is the same size.
- Every screenshot has a caption in `<sub>` saying what is in it and that it was made with fake
  agents. A reader must never mistake a demo for a measurement.
- The terminal uses the default `lane` palette and the same font at the same size in every shot.
  The one exception is `docs/img/themes.gif`, the README hero: it steps through every theme with
  the overview's `T` picker and hides the wall, so the backdrop and the tiles fill the right side.
- The GitHub social preview, `docs/img/social.png` (1280×640), is the hero under the lockup, a
  two-line tagline that says what fleet does, and the supported tools, built by
  `scripts/social-card.sh`. GitHub has no API for it: upload it under Settings, Social preview,
  after regenerating.

Diagrams are Mermaid, checked with mermaid-cli before committing:

- Nodes and edges take GitHub's own theme: set no `fill`, `stroke` or `color` anywhere in the
  diagram, so it stays legible on a white and a near-black page alike. Square nodes, labels in
  sentence case.
- Exactly one path may be emphasised: the one the surrounding text is about. Mark it with a thick
  edge, a bold label or the only double-bordered node, not with a hard-coded colour. Lane blue is
  2.1:1 on GitHub's dark ground, so it belongs only in a generated SVG figure that ships a light
  and a dark file behind a `<picture>`.
- Left to right for flow, top to bottom for layers. No more than about twelve nodes. A diagram that
  needs a legend has become two diagrams.
- The diagram repeats nothing the sentence above it already says. If the sentence is enough, delete
  the diagram.

## 6. Checklist before a docs commit

- [ ] Headings are sentence case, no em dashes anywhere in the diff.
- [ ] Every new claim has a number, a date or a pointer to the code.
- [ ] Planned work is marked planned.
- [ ] Tables are left-aligned, with units in the figure column.
- [ ] Shell examples contain no home path, no email, no private name.
- [ ] Marks are placed with `<picture>`, `height` only, with `alt` text.
- [ ] New screenshots were made with fake agents and carry a caption.
- [ ] `scripts/check-public.sh HEAD` passes.
