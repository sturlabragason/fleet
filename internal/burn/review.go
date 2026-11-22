package burn

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os/exec"
	"strings"
	"time"
)

// BranchSummary describes one fleet/burn-* branch for the morning review page.
type BranchSummary struct {
	Branch  string `json:"branch"`
	Commits int    `json:"commits"`
	Subject string `json:"subject"`
	Stat    string `json:"stat"`
}

// CollectBurnBranches inspects git in dir for local fleet/burn-* branches ahead of main.
func CollectBurnBranches(ctx context.Context, dir string) ([]BranchSummary, error) {
	out, err := gitOut(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads/fleet/burn-*")
	if err != nil {
		return nil, fmt.Errorf("list burn branches: %w", err)
	}
	base := "main"
	if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "main"); err != nil {
		base = "master"
	}
	var branches []BranchSummary
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		br := strings.TrimSpace(line)
		if br == "" {
			continue
		}
		rng := base + ".." + br
		logOut, err := gitOut(ctx, dir, "log", "--oneline", rng)
		if err != nil {
			continue
		}
		lines := nonEmptyLines(logOut)
		subject := "(no commits ahead of " + base + ")"
		if len(lines) > 0 {
			subject = lines[0]
		}
		stat, _ := gitOut(ctx, dir, "diff", "--stat", base+"..."+br)
		branches = append(branches, BranchSummary{
			Branch:  br,
			Commits: len(lines),
			Subject: subject,
			Stat:    strings.TrimSpace(stat),
		})
	}
	return branches, nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// RenderReviewHTML renders a self-contained light-mode Accept/Reject review page.
func RenderReviewHTML(generatedAt time.Time, branches []BranchSummary) (string, error) {
	var buf bytes.Buffer
	data := struct {
		Generated string
		Branches  []BranchSummary
	}{
		Generated: generatedAt.Format("2006-01-02 15:04 MST"),
		Branches:  branches,
	}
	if err := reviewTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render review html: %w", err)
	}
	return buf.String(), nil
}

var reviewTmpl = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>fleet burn — Morning Review</title>
<style>
  :root { --bg:#fafafa; --card:#fff; --ink:#111; --muted:#555; --line:#e2e4e8; --accent:#1b6ef3; --ok:#1a7f37; --no:#cf222e; }
  body { margin:0; font:15px/1.5 system-ui,-apple-system,sans-serif; background:var(--bg); color:var(--ink); padding:24px; max-width:900px; margin-inline:auto; }
  h1 { font-size:22px; margin:0 0 4px; }
  .meta { color:var(--muted); margin:0 0 20px; font-size:13px; }
  .card { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:16px; margin-bottom:14px; }
  .row { display:flex; justify-content:space-between; align-items:center; gap:12px; flex-wrap:wrap; }
  .branch { font-family:ui-monospace,monospace; font-weight:600; font-size:14px; }
  .picks { display:flex; gap:8px; }
  button.pick { border:1px solid var(--line); background:#fff; padding:6px 12px; border-radius:6px; cursor:pointer; font-weight:500; }
  button.pick[data-val="accept"].active { background:var(--ok); color:#fff; border-color:var(--ok); }
  button.pick[data-val="reject"].active { background:var(--no); color:#fff; border-color:var(--no); }
  pre { background:#f6f8fa; padding:10px; border-radius:6px; overflow:auto; font-size:12px; margin:10px 0 0; }
  .bar { margin-top:20px; display:flex; gap:12px; align-items:center; }
  button.submit { background:var(--accent); color:#fff; border:none; padding:10px 18px; border-radius:6px; font-weight:600; cursor:pointer; }
  #out { margin-top:12px; }
</style>
</head>
<body>
<h1>fleet burn — Morning Review</h1>
<p class="meta">Generated {{.Generated}} · {{len .Branches}} burn branch(es) · Work stays on branches until you merge.</p>
{{if not .Branches}}
<div class="card">No <code>fleet/burn-*</code> branches waiting for review.</div>
{{end}}
{{range .Branches}}
<div class="card" data-branch="{{.Branch}}">
  <div class="row">
    <div><span class="branch">{{.Branch}}</span> · {{.Commits}} commit(s)</div>
    <div class="picks">
      <button type="button" class="pick" data-val="accept" onclick="pick(this)">Accept</button>
      <button type="button" class="pick" data-val="reject" onclick="pick(this)">Reject</button>
    </div>
  </div>
  <p>{{.Subject}}</p>
  {{if .Stat}}<pre>{{.Stat}}</pre>{{end}}
</div>
{{end}}
<div class="bar">
  <button type="button" class="submit" onclick="exportPicks()">Copy Decisions JSON</button>
  <span id="status" class="meta" style="margin:0"></span>
</div>
<pre id="out" style="display:none"></pre>
<script>
function pick(btn) {
  const card = btn.closest('.card');
  card.querySelectorAll('.pick').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  card.dataset.choice = btn.dataset.val;
}
function exportPicks() {
  const picks = {};
  document.querySelectorAll('.card[data-branch]').forEach(c => {
    picks[c.dataset.branch] = c.dataset.choice || 'undecided';
  });
  const json = JSON.stringify({ generated: {{.Generated}}, decisions: picks }, null, 2);
  const out = document.getElementById('out');
  out.textContent = json;
  out.style.display = 'block';
  if (navigator.clipboard) {
    navigator.clipboard.writeText(json).then(() => {
      document.getElementById('status').textContent = 'Copied to clipboard.';
    }).catch(() => {});
  }
}
</script>
</body>
</html>
`))
