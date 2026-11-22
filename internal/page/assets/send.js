// Submit for a fleet page (ADR-197), the artifact host's Send protocol of
// the decision-page skill (ADR-163): a real form POST to /_answer/<page> with
// the token the host put in the page, once per version; it answers 303 back
// with ?sent= or ?send_error=. Offline, signed out or a local file: copy the
// answer line instead. A card's free text overrides its pick.
(() => {
  const f = document.getElementById('f');
  if (!f) return;
  const out = document.getElementById('reply'), copyBtn = document.getElementById('copy');
  const send = document.getElementById('send'), status = document.getElementById('status');
  const note = document.getElementById('note'), sendf = document.getElementById('sendf');
  const sets = () => [...f.querySelectorAll('fieldset[data-key]')];
  const q = s => '"' + s.trim().replace(/"/g, "'") + '"';
  function fields() {
    const o = [['decision', f.dataset.decision]];
    for (const fs of sets()) {
      const k = fs.dataset.key;
      if (k === 'defaults') {
        const off = [...fs.querySelectorAll('input.d')].map((c, i) => c.checked ? 0 : i + 1).filter(Boolean);
        o.push([k, off.length ? off.join('+') : 'ok']);
        continue;
      }
      const r = fs.querySelector('input[type=radio]:checked');
      if (r) o.push([k, r.value]);
      const t = fs.querySelector('textarea');
      if (t && t.value.trim()) o.push([k + '.text', t.value.trim()]);
    }
    if (note.value.trim()) o.push(['note', note.value.trim()]);
    return o;
  }
  function build() {
    const parts = [];
    for (const [k, v] of fields().slice(1)) {
      if (k === 'defaults') parts.push(v === 'ok' ? 'defaults ok' : 'defaults except ' + v);
      else if (k === 'note') parts.push('note ' + q(v));
      else if (k.endsWith('.text')) {
        const key = k.slice(0, -5), last = parts.length - 1;
        if (last >= 0 && parts[last].split(' ')[0] === key) parts[last] += ' ' + q(v);
        else parts.push(key + ' ' + q(v));
      } else parts.push(k + ' ' + v);
    }
    out.value = f.dataset.page + ': ' + parts.join(', ');
  }
  function mark() {
    for (const fs of sets()) {
      const t = fs.querySelector('textarea');
      fs.classList.toggle('over', !!(t && t.value.trim()));
    }
  }
  f.addEventListener('submit', e => e.preventDefault()); // Enter in a text box must not reload the page
  f.addEventListener('input', () => { mark(); build(); });
  f.addEventListener('change', build);
  const say = (text, ok) => { status.textContent = text; status.className = ok ? 'ok' : 'meta'; };
  async function copy() {
    try { await navigator.clipboard.writeText(out.value); } catch { out.select(); document.execCommand('copy'); }
  }
  copyBtn.addEventListener('click', async () => { build(); await copy(); copyBtn.textContent = 'Copied'; });
  const name = location.pathname.split('/')[1], key = 'answer:' + location.pathname;
  const inputs = () => [...f.querySelectorAll('input, textarea:not(#reply)')];
  const minted = sendf.elements.token.value !== '__ARTIFACT_SEND_' + 'TOKEN__';
  let reachable = false;
  if (/^https?:$/.test(location.protocol) && name && minted) {
    sendf.action = '/_answer/' + name;
    fetch(location.pathname, { method: 'HEAD', cache: 'no-store', redirect: 'error' })
      .then(r => { reachable = r.ok; }, () => {});
  }
  async function fallback(why) {
    copyBtn.hidden = false; await copy();
    say(why + ' Your answer is copied: paste it to the agent.');
  }
  sendf.addEventListener('submit', e => {
    build();
    if (!reachable || !navigator.onLine) {
      e.preventDefault();
      return fallback('Could not reach the server (offline, signed out or a local file).');
    }
    sendf.querySelectorAll('input:not([name=token])').forEach(i => i.remove());
    for (const [k, v] of fields()) {
      const i = document.createElement('input');
      i.type = 'hidden'; i.name = k; i.value = v; sendf.append(i);
    }
    sessionStorage.setItem(key, JSON.stringify(inputs().map(i => [i.checked, i.value])));
    sessionStorage.setItem(key + ':y', String(window.scrollY));
    send.disabled = true; send.textContent = 'Sending…';
  });
  const back = new URLSearchParams(location.search);
  if (back.has('sent') || back.has('send_error')) {
    try {
      const saved = JSON.parse(sessionStorage.getItem(key) || '[]');
      inputs().forEach((i, n) => { if (saved[n]) { i.checked = saved[n][0]; i.value = saved[n][1]; } });
    } catch { /* nothing saved: show the defaults */ }
    const y = Number(sessionStorage.getItem(key + ':y'));
    if (y) { history.scrollRestoration = 'manual'; window.scrollTo(0, y); }
    const err = back.get('send_error');
    if (!err || err === 'duplicate') {
      send.disabled = true; send.textContent = 'Sent ✓';
      say(err ? 'An answer was already sent for this version of the page. To change it, tell the agent in chat.'
        : 'Sent and saved. The agent takes it; if it has finished, fleet hands it to the admiral within a couple of minutes. You can close this page.', true);
    } else fallback('The server did not accept it.');
  }
  mark(); build();
})();
