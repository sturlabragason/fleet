// Load a page headless and fail if any image did not decode.
// Usage: node scripts/check-renders.mjs <url> [--min-images N]
//
// Chromium comes from playwright-core. Point FLEET_PLAYWRIGHT_CORE at its
// index.mjs to override; otherwise we try a normal import and then the global
// npm root, because playwright-cli's own daemon insists on a Chrome channel
// that is usually not installed.
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

function globalNpmRoot() {
  try {
    return execFileSync('npm', ['root', '-g'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
  } catch {
    return '';
  }
}

async function loadChromium() {
  const override = process.env.FLEET_PLAYWRIGHT_CORE;
  if (override) {
    if (!existsSync(override)) throw new Error(`FLEET_PLAYWRIGHT_CORE does not exist: ${override}`);
    return (await import(pathToFileURL(override).href)).chromium;
  }
  for (const spec of ['playwright-core', 'playwright']) {
    try {
      return (await import(spec)).chromium;
    } catch {
      /* not resolvable from here; try the next candidate */
    }
  }
  const root = globalNpmRoot();
  const candidates = root
    ? [
        join(root, 'playwright-core', 'index.mjs'),
        join(root, 'playwright', 'index.mjs'),
        join(root, '@playwright', 'cli', 'node_modules', 'playwright-core', 'index.mjs'),
        join(root, '@playwright', 'test', 'node_modules', 'playwright-core', 'index.mjs'),
      ]
    : [];
  for (const path of candidates) {
    // playwright-core resolves its browsers relative to itself, so import the file directly.
    if (existsSync(path)) return (await import(pathToFileURL(path).href)).chromium;
  }
  throw new Error(
    'playwright-core not found. Install it (npm i -g playwright-core) or set ' +
      'FLEET_PLAYWRIGHT_CORE to its index.mjs.' +
      (candidates.length ? `\nTried:\n  ${candidates.join('\n  ')}` : ''),
  );
}

const args = process.argv.slice(2);
const minIdx = args.indexOf('--min-images');
const url = args.find((a, i) => !a.startsWith('--') && !(minIdx !== -1 && i === minIdx + 1));
if (!url) {
  console.error('usage: node check-renders.mjs <url> [--min-images N]');
  process.exit(2);
}
const minImages = minIdx === -1 ? 1 : Number(args[minIdx + 1]);
if (!Number.isInteger(minImages) || minImages < 0) {
  console.error('--min-images needs a non-negative integer');
  process.exit(2);
}
for (const a of args) {
  if (a.startsWith('--') && a !== '--min-images') {
    console.error(`unknown flag: ${a}`);
    process.exit(2);
  }
}

let chromium;
try {
  chromium = await loadChromium();
} catch (e) {
  console.error(e.message);
  process.exit(2);
}

// The page's own directory, so failure lines name files rather than temp paths.
const base = url.replace(/[^/]*$/, '');
const short = (u) => (u.startsWith(base) ? u.slice(base.length) : u).slice(0, 70);

let browser;
let r;
const failed = [];
try {
  browser = await chromium.launch();
  const page = await browser.newPage();
  page.on('requestfailed', (q) => failed.push(short(q.url())));
  await page.goto(url, { waitUntil: 'networkidle' });
  // Images appended by script after load are in neither list until they settle.
  await page.waitForTimeout(300);
  r = await page.evaluate(() => {
    const imgs = [...document.images];
    const decoded = (i) => i.naturalWidth > 0;
    return {
      total: imgs.length,
      ok: imgs.filter(decoded).length,
      broken: imgs.filter((i) => !decoded(i)).map((i) => (i.getAttribute('src') || '(none)').slice(0, 70)),
      dims: imgs.filter(decoded).map((i) => `${i.naturalWidth}x${i.naturalHeight}`),
    };
  });
} catch (e) {
  console.error(e.message);
  process.exitCode = 2;
} finally {
  await browser?.close();
}
if (!r) process.exit(2);
console.log(JSON.stringify({ ...r, failedRequests: failed }, null, 1));
if (r.total < minImages) {
  console.error(`expected at least ${minImages} image(s), the page has ${r.total}`);
  process.exit(1);
}
process.exit(r.broken.length === 0 && failed.length === 0 ? 0 : 1);
