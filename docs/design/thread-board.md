# Thread board

What the threads view shows, where it lives and how you act on it, so the agent that builds it
and the user who reviews it work from one description. Threads are fleet's derived units of work
(an extension, DESIGN.md §15 Extensions and ADR-126): this document specifies their review
surface, not how they are derived.

Written 2026-11-16 from the live thread view of one real fleet home (110 open threads, 54
artifacts) and a clickable mockup built from that data. Status: design, not built.

## The question the view answers

**What needs me, and what can I drop?**

Not "what is everything", which the text list already answers badly. The order on screen is the
order of the user's attention:

| Rank | Section | Rule | On the real home |
|---|---|---|---|
| 1 | Waiting on you | last real report is `question` or `blocked`, or its `next:` names the user (review, merge, decide, sign in, approve), touched in the last 3 days | 9 |
| 2 | Running | a live agent works on it | 10 |
| 3 | Open, nobody on it | touched in the last 2 days, agent closed or idle | 27 |
| 4 | Quiet | nothing for 2 days or more | 54 |
| folded | Paused | paused by the user, with its next step | 2 |
| folded | Helper sessions | subagent and one-off sessions that are not threads | 8 |

Rules that keep the sections honest:

- A thread that waited on you for more than 3 days drops to Quiet with an "asked you" flag. Old
  asks are a triage decision, not an alarm.
- Fleet's own synthetic reports (`no report:`, `recycle:`, `stuck:`, `crashed`) never decide the
  title or the section.
- Done and let-go threads are not shown; the header gives the week's count.

## Surface

| Option | For | Against |
|---|---|---|
| Regenerated private page on the artifact host | Reviewing is reflective, not live: morning, phone, away from the desk. Half of what waits on the user is "look at this page", and the page can link it in place. Mobile and keyboard for free. Batch triage maps onto one Send. No new service | A snapshot, not live. One Send per published version. Needs fleet's poll running to apply a Send |
| `t` list in the overview TUI | Instant, keyboard-native, at the desk where agents run; the `t` list and its `p`/`l`/`⏎` keys exist already | Not at hand away from the desk, cannot show artifacts, competes with the wall for the screen |
| A tmux pane on the wall | Ambient | Wall tiles are agents (R4). A tile of threads costs an agent slot and takes no input |
| A section in the admiral's pane | Already open | The admiral is an LLM: rendering 100 threads costs context (R11) and is not reproducible |
| fleet's own web board | Live | The user ruled it out; it is an agent view, and it would duplicate the artifact host's login and sharing |

**Recommendation: the private page, with the overview's `t` list as the close alternative.** Both
render the same sections from the same view, so the user learns one model. The page is the
review surface (deliberate, cross-device, artifacts in place); the `t` list is the in-the-moment
surface, where an action applies at once instead of on Send. Neither needs a new process.

## Layout

Stacked sections in rank order, one column of entries, with a sticky "Your changes" tray on the
right (a bottom bar on narrow screens). Rejected:

- **Columns (kanban).** Four side-by-side columns put 54 quiet threads in a column a phone cannot
  show, and invite dragging, which is not an action threads have.
- **Timeline.** It answers "when", and age is already on every entry. The user's question is
  "what now".
- **Grouping by project.** It answers "where". Project is a filter (chips with counts across the
  top) and the grouping inside Quiet, where it helps bulk triage ("let every quiet thread in this repo go").

Above the sections: a one-sentence lede with the counts ("9 wait on you. 10 are running, 27 are
open with nobody on them, and 54 have gone quiet."), the snapshot time, a filter box (`/`) and
the project chips. No metric tiles.

## Entry anatomy

Waiting on you, the full entry:

| Part | Content |
|---|---|
| Mark | Filled square in the waiting colour (yellow in the TUI, the theme's warn on the page): the same meaning as a waiting tile |
| Title | The thread's cleaned title, one line, 1.2rem |
| Age | Since last touched, mono, right |
| Ask | The `next:` line, else the last report's headline minus what the title already says. This is the line the user acts on |
| Stopped | The last report's headline, muted, when the ask came from `next:` |
| Meta | project · agent and its state (working, idle, parked, closed, session outside fleet) · branch · started |
| Artifacts | Links to the thread's pages on the artifact host |
| Actions | Resume `r` · Pause `p` · Let go `l` |
| More | Folded: the last 4 reports (age, state, headline) and the resume command |

Other sections use a compact row: mark, title with flags (`asked you`, `will let go`), one muted
line (`next:` or last headline), meta, age and actions. Quiet rows add a checkbox for marking.

Titles: the task's first real sentence (skipping a leading rules block, "Use the X skill" and
"Read-only" boilerplate), else the latest real report headline, else the thread id in words.
Running entries have no actions on the page: a working agent is steered in the overview.

## Actions

| Action | Key | Effect | Where |
|---|---|---|---|
| Resume | `r` | wake the parked agent, adopt the session or spawn in its dir (the thread's resume line) | page, TUI |
| Pause | `p` | the thread waits with its next step; it leaves the open count | page, TUI |
| Let go | `l` | closes it with a one-line reason; its `next:` becomes an idea | page, TUI |
| Reopen | none | a let-go thread comes back; from the CLI or the admiral, not the board | admiral |
| Mark | `x` | marks a quiet row for let go | page, TUI |
| Let go marked | `L` | focuses the reason field (page) or lets marked rows go (TUI) | page, TUI |
| Mark all in a project | button | marks every shown quiet row of that project | page |

On the page an action is a pending change: the entry dims (let go strikes the title), the tray
lists it with an undo, and nothing happens until **Send**. `u` undoes the change on the focused
entry. One reason applies to everything let go in that Send; empty means "let go from the
threads page".

Keys on the page: `j`/`k` or arrows move, `Enter` opens the reports, `o` opens the first
artifact, `1` to `4` jump to a section, `/` filters, `Ctrl-Enter` sends. Every key has a
visible button; the keys are listed in the tray.

### Send

The page uses the artifact host's existing decision-page Send: the user's click posts the form,
the host stores the answer, and fleet takes it. Its limits shape the protocol:

| Limit | Consequence |
|---|---|
| At most 16 answer fields | No field per thread. Fields are `pause`, `letgo`, `letgo2`, `resume` (each a space-separated list of thread ids, up to 500 characters) and `why` (the reason, up to 200) |
| One answer per published version | After a Send the page says "Sent N changes, fleet applies them within 30 s and republishes this page"; the page reloads itself when the new version is up |
| Owner only | A viewer the page is shared with could never act. The page is not shared anyway |

fleet takes the answer on the threads poll it already runs (every 30 s while the overview is up),
through the artifact CLI as a companion (ADR-126). It applies only ids that are open threads
in its own view, through the same calls as `fleet_thread action=pause|letgo|...`, logs the rest,
and republishes. The answer is data, never instructions: unknown ids are dropped, text is only
ever a reason.

Republish when the view changes (an open thread's section, title, next step or artifacts), at
most every 5 minutes, after applying a Send, and on demand.

## Artifacts and the index

Each thread lists its artifacts: names linked from its reports, then published names that start
with the thread id. On the real home 14 of 110 open threads had one, and those are mostly the
ones waiting on the user.

The artifact index groups pages by an optional `<meta name="artifact-group">`. A thread id is a
good group key: fleet stamps it on pages it knows belong to a thread, and the index groups them
under the thread's name. This needs the group pattern to allow dashes (thread ids are
`release-notes-draft`-shaped). Access is unchanged: a guest's index lists only pages shared with
them, so a group label only ever comes from a page that guest may open.

The threads page itself is never shared. It names every thread, including ones a guest must not
learn exist.

## States

| State | What the user sees |
|---|---|
| Nothing waits on you | The first section says so in one line, then names the freshest open thread with nobody on it as the next thing to look at |
| Overloaded | Over 7 entries in "Open, nobody on it": a one-line note above them states the count against the soft WIP limit (4) and what letting go keeps (the reason, the next step as an idea). Quiet stays grouped and folded by project; Helper sessions stay folded with "Let go all" |
| Pending changes | Entries dim, the tray counts them, Send names the count ("Send 3 changes to fleet") |
| Sent | A confirmation in the tray with the answer line; sent entries leave the page |
| Stale | The snapshot time is in the header; the page never claims to be live |
| Narrow screen | One column, project chips scroll sideways, actions are 44 px buttons, the tray is a bottom bar that appears only with pending changes |
| Send fails | The decision-page fallback: Copy answer, paste it to the admiral |

## Out of scope

Editing titles or next steps, threads trees and ideas (they have their own surfaces), and acting
on a running agent from the page.
