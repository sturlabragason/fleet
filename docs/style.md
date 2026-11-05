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
redraw either one, and never recolour, stretch, rotate, outline or shadow them.

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

**The TUI** uses [Catppuccin Mocha](https://catppuccin.com/palette) (`internal/theme`), and should
keep using it. Lane blue is a print and web accent: `#0033CC` on the Mocha base `#1e1e2e` is
1.8:1, which is unreadable in a terminal. The brand rule that carries over is
not the hex value, it is the discipline: colour means status, never decoration, and at most one
thing per screen is allowed to be the accent.

The mapping from the house roles to the terminal palette:

| House role | Document token | TUI constant | Terminal colour |
|---|---|---|---|
| Accent, the one thing that matters | Lane `#0033CC` | `theme.Select` | Lavender `#b4befe` |
| Body text | Ink `#161616` | `theme.Text` | `#cdd6f4` |
| Secondary text, resting state | Grey `#525252` | `theme.Idle` | Overlay1 `#7f849c` |
| Inactive, finished with | Hair `#E0E0E0` | `theme.Gone` | Surface2 `#585b70` |
| Signal, something is wrong | Signal `#C40000` | `theme.Error` | Red `#f38ba8` |
| Ground | Paper `#FFFFFF` | `theme.Base` | `#1e1e2e` |

Status colours carry meaning, and that meaning is fixed:

| Status | Constant | Colour | Means |
|---|---|---|---|
| `working` | `theme.Working` | Green `#a6e3a1` | Running, needs nothing from you |
| `waiting` | `theme.Waiting` / `WaitingAlt` | Yellow `#f9e2af`, pulsing to Peach `#fab387` | Wants an answer from you |
| `error` | `theme.Error` | Red `#f38ba8` | Failed |
| `stuck` | `theme.Stuck` | Maroon `#eba0ac` | Grinding: the same error twice, or no output for a long time |
| `idle` | `theme.Idle` | Overlay1 `#7f849c` | Alive, nothing to do |
| `gone` | `theme.Gone` | Surface2 `#585b70` | Process ended |
| `queued` | `theme.Gone` | Surface2 `#585b70` | Waiting for a slot, no pane yet |
| `starting` | `theme.Working` | Green `#a6e3a1` | Launching |

Selection, focus and the target slot are three different things, so they look different (ADR-37).
Lavender is spent on exactly one of them:

| State | Looks like | Constant |
|---|---|---|
| `▶` target slot (the next card lands here) | Bold `▶` before the pane title and after the card name | `theme.Select` Lavender `#b4befe` |
| Overview cursor (what `⏎` would act on) | An underline, in the cell's own status colour, plus a thick card border | the status colour, or `theme.Text` when that colour recedes (`gone`, `queued`) |
| Active tmux pane (where your keys go) | Heavy border, full-brightness text | `theme.Text` `#cdd6f4` |
| Any other pane | One step dimmer, same ground | Overlay2 `#9399b2` |
| Pinned `⚑`, `+N hidden`, `+N idle`, one-line row ages | Resting grey | `theme.Muted` / `theme.Idle` Overlay1 `#7f849c` |
| `· next out` (the pane a card would evict) | Dimmest, it is on its way out | `theme.Gone` Surface2 `#585b70` |

Dimming unfocused panes and the emphasis pane (ADR-16, off by default, `fleet wall emphasis`) must not
stack into "big but unreadable": emphasis only changes a pane's size, dimming only its text, and
Overlay2 stays at 5.8:1 on the base so an emphasised pane you are not typing in is still legible.
Do not take the dim step down to Overlay1 (4.4:1), which is the resting colour of things you are
meant to ignore. A card-to-pane highlight (`@fleet_hl` in docs/ux-interaction.md) is not built; when
it is, it is Overlay1, not a second accent.

Rules for anyone changing `internal/theme`:

- A new colour is a new meaning. If it does not change what the user should do, it is decoration:
  use an existing role instead.
- Green and red are never the only difference between two states a colour-blind reader must tell
  apart. Every tile carries its status word and a distinct glyph (`working` a spinner, `waiting`
  `●`, `error` `✖`, `stuck` `⧗`, `idle` `○`, `gone` `·`, `queued` `‖`, `starting` `▸`), tiles are
  ordered by attention, and `waiting` pulses. Colour is the fastest signal, never the only one: a
  new status needs its own glyph before it needs its own colour.
- Lightness carries the hierarchy, so it survives any colour vision. Against the Mocha base
  `#1e1e2e`, the four statuses with a live process (`working`, `waiting`, `error`, `stuck`) sit
  between 7.1:1 (`error`) and 12.9:1
  (`waiting`), while `idle` drops to 4.4:1 and `gone` to 2.5:1 on purpose: they recede. Keep a new
  status above 7:1 if it should be noticed, and don't lift `idle` or `gone`.
- The dim step of the running-subagent wave (`theme.WorkingDim`, `#628067`, 3.8:1) stays a darker
  green, never grey, so it cannot be read as a finished dot.
- Team colours come from `theme.TeamPalette` and must stay out of the status range: no green,
  yellow, peach, red or maroon, and not the selection lavender.

## 5. Screenshots and diagrams

Screenshots:

- Made with fake agents (`scripts/screenshot.sh`), never with a real session. A real pane leaks
  repository names, paths and prompts, and `check-public.sh` cannot see inside a PNG.
- Agent names come from the generic set used elsewhere in the docs (`api-auth`, `web-nav`,
  `infra-dns`). No client or employer words, ever.
- One window, full fleet layout, no desktop around it, no window shadow, no rounded corners, no
  device frame, no cursor.
- Every screenshot has a caption in `<sub>` saying what is in it and that it was made with fake
  agents. A reader must never mistake a demo for a measurement.
- The terminal uses the Mocha palette and the same font at the same size in every shot.

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
