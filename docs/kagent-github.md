# GitHub access for kagent

A kagent agent runs in a sandbox with no route to GitHub. It clones and pushes over HTTPS through a kagent
EgressPolicy, which adds the credential on the way out, so the agent never sees a token. Fleet's part is to keep a
token in the Secret that policy reads (DESIGN §7d, ADR-146).

## 1. The cluster side

An EgressPolicy for `https://github.com` that injects the Secret, and the Agent that uses it:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: EgressPolicy
metadata: {name: github, namespace: kagent}
spec:
  destinations:
    - origin: https://github.com
      authentication:
        header: Authorization
        prefix: "Basic "
        secretKeyRef: {name: github-push, key: authorization}
---
# in the Agent: spec.egressPolicyRefs: [{name: github}]
```

The gateway replaces an existing header and does not add one. Tell the agent (its system prompt) to run git with
`-c http.extraHeader="Authorization: Basic kagent-credential-injected"`. Don't create the Secret yourself: fleet
writes it, and a GitOps tool that also manages it will fight fleet.

## 2. The profile

Add `--github-secret` to the kagent profile's `acp` args in `~/.fleet/tools.toml`:

```toml
[kagent-prod]
command = "{fleet}"
driver = "acp"
acp_from = "kagent"
acp = ["--context", "prod", "--agent", "kagent/claude", "--github-secret", "kagent/github-push"]
```

When the agent's pane starts, fleet gets a token, writes it into that Secret with `kubectl apply --server-side`
under your kubeconfig, and gets a new one every 30 minutes while the pane is open. When there is no token, the spawn
fails and says where to put one.

kagent v1.0.1 reads the Secret once per conversation, at its first GitHub request, and keeps that token. A new
conversation gets the current token. One that first talked to GitHub more than about an hour ago gets `401` on a later
push with an App token: spawn a new agent, or use a token file with a long-lived token.

## 3. The token

Choose one.

**A token file.** Put a fine-grained personal access token (the repos you want, Contents read and write) in
`~/.config/fleet/github-token`:

```sh
mkdir -p ~/.config/fleet && umask 077 && wl-paste > ~/.config/fleet/github-token   # or pbpaste on macOS
```

Another path: `--github-token-file <path>`. The file must be readable by you only.

**A GitHub App (recommended).** An App's tokens last an hour and work on the repos you install it on. The private
key stays on your machine.

1. Create and install the App. The [setup script](../examples/github-app-setup.py) uses GitHub's manifest flow
   on localhost and opens your browser twice. Click **Create GitHub App**, then pick the repositories and click
   **Install**:

   ```sh
   python3 examples/github-app-setup.py            # --org <org> to create it under an organization
   ```

   It asks for Contents read/write and Metadata read only, with no webhook. It writes `app-id`, `client-id`,
   `installation-id` and `private-key.pem` (mode 600) to `~/.config/fleet/github-app/` and never prints the key.
   To add repositories later, open the App's page on GitHub and choose **Install App** again.
2. Install a minting tool. The [`gh token`](https://github.com/Link-/gh-token) extension does it:
   `gh extension install Link-/gh-token`.
3. Point the profile at it:

   ```toml
   acp = ["--context", "prod", "--agent", "kagent/claude", "--github-secret", "kagent/github-push",
          "--github-token-cmd", "d=~/.config/fleet/github-app; gh token generate --app-id $(cat $d/app-id) --installation-id $(cat $d/installation-id) --key $d/private-key.pem --token-only"]
   ```

Any command that prints one token works the same way, for example one that reads a vault.

## Keep the token off your main branch

A token with Contents write can push any branch it can reach. Add a branch ruleset to each repo: target the
default branch, turn on Restrict updates, Restrict deletions and Block force pushes, and put only the Repository
admin role on the bypass list. The App is then not on the list, so its token cannot change the default branch, and the
agent's `fleet/` branches still push.

## When it fails

| Message | Fix |
|---|---|
| `no GitHub token: ... does not exist` | Add the token file, or add `--github-token-cmd`. |
| `... is readable by others; chmod 600 it` | `chmod 600 <file>` |
| `github token command: ...` | Run the command yourself; it must print one token and exit 0. |
| `github secret ns/name: ... forbidden` | Your kube user needs to create and patch Secrets in that namespace. |
| The agent's push gets `403` | The App or token lacks Contents write on that repo, or a ruleset blocks the branch. |
