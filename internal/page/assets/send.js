// Submit for a fleet page (ADR-197), the artifact host's Send protocol of
// the decision-page skill (ADR-163): a real form POST to /_answer/<page> with
// the token the host put in the page, once per version; it answers 303 back
// with ?sent= or ?send_error=. A card's free text overrides its pick.
// ADR-199: what the user types is saved in this browser as they type and
// restored on load, and a status bar polls version.json, because a republish
// voids this tab's token, a lost sign-in sends the POST to the login page and
// a deleted page takes nothing. When Submit can't work, the page says why
// before the click and offers check again, copy and download.
(() => {
  const f = document.getElementById('f');
  if (!f) return;
  const $ = id => document.getElementById(id);
  const out = $('reply'), send = $('send'), status = $('status'), note = $('note'), sendf = $('sendf');
  const live = $('live'), liveText = $('live-text'), reload = $('reload'), rescue = $('rescue');
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

  // Autosave by card key and option value, not position, so it survives a republish.
  const store = 'fleet-page:' + location.pathname;
  const load = () => { try { return JSON.parse(localStorage.getItem(store)) || {}; } catch { return {}; } };
  const patch = fields => {
    try { localStorage.setItem(store, JSON.stringify({ ...load(), ...fields })); } catch { /* private window or full: Copy still works */ }
  };
  function save(extra) {
    const cards = {};
    for (const fs of sets()) {
      const r = fs.querySelector('input[type=radio]:checked'), t = fs.querySelector('textarea');
      cards[fs.dataset.key] = fs.dataset.key === 'defaults'
        ? [...fs.querySelectorAll('input.d')].map(c => c.checked)
        : { pick: r ? r.value : '', text: t ? t.value : '' };
    }
    patch({ decision: f.dataset.decision, saved: Date.now(), cards, note: note.value, ...extra });
  }
  function restore(rec) {
    if (!rec.cards || (rec.sent && rec.decision !== f.dataset.decision)) return false; // answered an older round
    for (const fs of sets()) {
      const c = rec.cards[fs.dataset.key];
      if (!c) continue;
      if (Array.isArray(c)) {
        const boxes = fs.querySelectorAll('input.d');
        if (boxes.length === c.length) boxes.forEach((b, i) => { b.checked = c[i]; });
        continue;
      }
      const r = [...fs.querySelectorAll('input[type=radio]')].find(i => i.value === c.pick);
      if (r) r.checked = true;
      const t = fs.querySelector('textarea');
      if (t) t.value = c.text || '';
    }
    note.value = rec.note || '';
    return true;
  }

  const hm = t => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  const name = location.pathname.split('/')[1];
  const minted = sendf.elements.token.value !== '__ARTIFACT_SEND_' + 'TOKEN__';
  const online = /^https?:$/.test(location.protocol) && !!name && minted;
  const version = location.pathname.replace(/[^/]*$/, '') + 'version.json';
  const kept = ' What you typed is saved in this browser.';
  let state = 'checking', checked = 0, newer = '', failed = false;
  const lines = {
    checking: ['ok', () => 'Checking that Submit can reach the agent…'],
    open: ['ok', () => 'Open: Submit works until the agent republishes or deletes this page.' + kept],
    answered: ['ok', () => `Answered at ${hm(load().sent)}. To change it, tell the agent in chat.`],
    updated: ['bad', () => `The agent updated this page at ${hm(newer)}, after you opened it, so Submit here would be refused. Reload to get the new version: what you typed comes back.`],
    signedout: ['bad', () => 'Your sign-in has lapsed, so Submit would fail. Reload to sign in again: what you typed comes back.'],
    offline: ['bad', () => 'You are offline, so Submit would fail. Press Check again when you are back online.' + kept],
    gone: ['bad', () => 'This page was deleted, so Submit can\'t reach the agent. Copy your answers and paste them to the agent.'],
    error: ['warn', () => 'The server did not answer the check. Press Check again in a minute.' + kept],
    local: ['warn', () => 'This copy can\'t submit (a local file or a page without Send). Copy your answers and paste them to the agent.'],
  };
  const say = (text, tone) => { status.textContent = text; status.className = tone; };
  function show(s) {
    state = s;
    const [tone, text] = lines[s];
    live.hidden = false;
    live.className = 'live ' + tone;
    liveText.textContent = text();
    reload.hidden = s !== 'updated' && s !== 'signedout';
    rescue.hidden = tone === 'ok' && !failed;
    if (s === 'answered') send.disabled = true;
  }
  const sentHere = () => { const r = load(); return !!r.sent && r.sentVersion === f.dataset.version; };
  async function check() {
    if (!online) return show('local');
    let s = 'error';
    try {
      const signal = AbortSignal.timeout ? AbortSignal.timeout(8000) : undefined;
      const r = await fetch(version, { cache: 'no-store', redirect: 'manual', signal });
      // A lapsed sign-in redirects to the login host: an opaque redirect, not a throw.
      if (r.type === 'opaqueredirect' || r.status === 401 || r.status === 403) s = 'signedout';
      else if (r.status === 404) s = 'gone';
      else if (r.ok) {
        newer = (await r.json()).version;
        s = newer !== f.dataset.version ? 'updated' : sentHere() ? 'answered' : 'open';
      }
    } catch {
      s = navigator.onLine ? 'error' : 'offline';
    }
    checked = Date.now();
    show(s);
  }

  async function copy() {
    build();
    try { await navigator.clipboard.writeText(out.value); } catch { out.select(); document.execCommand('copy'); }
  }
  $('copy').addEventListener('click', async () => { await copy(); say('Copied. Paste it to the agent or the admiral.', 'ok'); });
  $('download').addEventListener('click', () => {
    build();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([out.value + '\n'], { type: 'text/plain' }));
    a.download = f.dataset.page + '-answers.txt';
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  });
  $('recheck').addEventListener('click', check);
  reload.addEventListener('click', () => { save({ y: window.scrollY }); location.assign(location.pathname); });

  f.addEventListener('submit', e => e.preventDefault()); // Enter in a text box must not reload the page
  f.addEventListener('input', () => { mark(); build(); save(); });
  f.addEventListener('change', () => { build(); save(); });
  if (online) sendf.action = '/_answer/' + name;
  sendf.addEventListener('submit', e => {
    build();
    save();
    if (state !== 'open' || Date.now() - checked > 45000 || !navigator.onLine) {
      // Not known to be open: check now and let the user press again, so the POST stays a real click.
      e.preventDefault();
      check().then(() => {
        if (state === 'open') say('Checked again: the page is still open. Press Submit again.', 'ok');
        else { failed = true; show(state); say(lines[state][1](), 'bad'); }
      });
      return;
    }
    sendf.querySelectorAll('input:not([name=token])').forEach(i => i.remove());
    for (const [k, v] of fields()) {
      const i = document.createElement('input');
      i.type = 'hidden'; i.name = k; i.value = v; sendf.append(i);
    }
    save({ y: window.scrollY });
    send.disabled = true; send.textContent = 'Sending…';
  });

  const rec = load(), back = new URLSearchParams(location.search);
  const restored = restore(rec);
  if (rec.y) {
    history.scrollRestoration = 'manual';
    window.scrollTo(0, rec.y);
    patch({ y: 0 });
  }
  if (back.has('sent') || back.has('send_error')) {
    const err = back.get('send_error');
    if (!err || err === 'duplicate') {
      patch({ sent: Number(back.get('sent')) * 1000 || Date.now(), sentVersion: f.dataset.version });
      send.disabled = true; send.textContent = 'Sent ✓';
      say(err ? 'An answer was already sent for this version of the page. To change it, tell the agent in chat.'
        : 'Sent and saved. The agent takes it; if it has finished, fleet hands it to the admiral within a couple of minutes. You can close this page.', 'ok');
    } else {
      failed = true;
      say('The server did not accept the answer. What you typed is kept: press Check again and Submit, or copy your answers and paste them to the agent.', 'bad');
    }
    history.replaceState(null, '', location.pathname); // F5 later must not count a newer version as sent
  } else if (restored && rec.saved) say(`Restored what you typed at ${hm(rec.saved)}.`, 'ok');
  if (online) {
    const checkIfVisible = () => { if (!document.hidden) check(); };
    setInterval(checkIfVisible, 30000);
    document.addEventListener('visibilitychange', checkIfVisible);
    addEventListener('online', check);
    addEventListener('offline', () => show('offline'));
    addEventListener('pageshow', e => { // back from a refused Submit: the cached page still says Sending…
      if (!e.persisted) return;
      send.disabled = false; send.textContent = 'Submit';
      check();
    });
  }
  mark(); build(); check();
})();
