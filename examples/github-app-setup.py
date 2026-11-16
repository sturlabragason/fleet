#!/usr/bin/env python3
"""Create a private GitHub App for kagent pushes with GitHub's manifest flow.

Run it on the machine that runs fleet. It opens your browser twice: once to
create the App (you click "Create GitHub App"), once to install it (you pick
the repos). It writes app-id, client-id, installation-id and private-key.pem
(mode 600) to --dir, and never prints the key. Fleet does not read these: a
token command does (docs/kagent-github.md, DESIGN ADR-146).

  python3 examples/github-app-setup.py [--org ORG] [--name NAME] [--dir DIR]

Needs python3 and a browser; GitHub's conversion endpoint needs no sign-in.
"""

import argparse
import html
import http.server
import json
import os
import secrets
import shutil
import subprocess
import threading
import urllib.parse
import urllib.request

PERMISSIONS = {"contents": "write", "metadata": "read"}


def open_url(url):
    for opener in ("xdg-open", "open"):
        if shutil.which(opener):
            subprocess.run([opener, url], check=False)
            return
    print(f"open this in a browser: {url}")


def serve(handler_state):
    """Serve on 127.0.0.1 until the handler has what it waits for."""

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, body):
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(body.encode())

        def do_GET(self):
            u = urllib.parse.urlparse(self.path)
            q = dict(urllib.parse.parse_qsl(u.query))
            page = handler_state["routes"].get(u.path)
            if page is None:
                self.send_error(404)
                return
            body, done = page(q)
            self.reply(body)
            if done:
                handler_state["done"].set()

    srv = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--org", help="create the App under this organization, not your account")
    ap.add_argument("--name", default=f"fleet-kagent-{secrets.token_hex(3)}", help="App name (unique on GitHub)")
    ap.add_argument("--dir", default=os.path.join(os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config"), "fleet", "github-app"))
    args = ap.parse_args()

    state = {"routes": {}, "done": threading.Event(), "secret": secrets.token_urlsafe(16)}
    srv = serve(state)
    base = f"http://127.0.0.1:{srv.server_port}"
    manifest = {
        "name": args.name,
        "url": "https://github.com/sturlabragason/fleet/blob/main/docs/kagent-github.md",
        "description": "Pushes branches for fleet's kagent agents.",
        "hook_attributes": {"url": "https://example.invalid/", "active": False},
        "redirect_url": f"{base}/created",
        "setup_url": f"{base}/installed",
        "public": False,
        "default_permissions": PERMISSIONS,
        "default_events": [],
    }
    owner = f"organizations/{args.org}/" if args.org else ""
    action = f"https://github.com/{owner}settings/apps/new?state={state['secret']}"
    result = {}

    def start(_):
        return (f"""<!doctype html><title>Create GitHub App</title>
<form id=f method=post action="{html.escape(action)}">
<input type=hidden name=manifest value="{html.escape(json.dumps(manifest))}">
<p>Sending you to GitHub to create the App <b>{html.escape(args.name)}</b>...</p>
<button>Continue</button></form><script>document.getElementById('f').submit()</script>""", False)

    def created(q):
        if "code" in result or q.get("state") != state["secret"] or "code" not in q:
            return ("<p>Unexpected request; ignored.</p>", False)
        result["code"] = q["code"]
        return ("<p>App created. You can close this tab; the install page opens next.</p>", True)

    def installed(q):
        if "app" not in result or "installation" in result or not q.get("installation_id", "").isdigit():
            return ("<p>No installation id in the request.</p>", False)
        result["installation"] = q["installation_id"]
        return ("<p>Installed. You can close this tab.</p>", True)

    state["routes"] = {"/": start, "/created": created, "/installed": installed}

    print('Next: click "Create GitHub App" in the browser tab that opens.')
    open_url(base + "/")
    state["done"].wait()

    req = urllib.request.Request(f"https://api.github.com/app-manifests/{result['code']}/conversions",
                                 method="POST", headers={"Accept": "application/vnd.github+json"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        app = json.load(resp)

    os.umask(0o077)
    os.makedirs(args.dir, mode=0o700, exist_ok=True)
    with open(os.path.join(args.dir, "private-key.pem"), "w") as f:
        f.write(app["pem"])
    for name, value in (("app-id", app["id"]), ("client-id", app["client_id"])):
        with open(os.path.join(args.dir, name), "w") as f:
            f.write(f"{value}\n")
    result["app"] = app["slug"]
    print(f"App {app['slug']} created (id {app['id']}); key and ids are in {args.dir}.")

    state["done"].clear()
    print("Next: pick the repositories and click Install in the browser tab that opens.")
    open_url(f"{app['html_url']}/installations/new")
    state["done"].wait()
    srv.shutdown()
    with open(os.path.join(args.dir, "installation-id"), "w") as f:
        f.write(f"{result['installation']}\n")
    print(f"Installed (installation {result['installation']}). Done.")


if __name__ == "__main__":
    main()
