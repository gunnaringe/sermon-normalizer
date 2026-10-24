# Access: which churches a user sees

There are two ways to run the studio, and one of them must be chosen: the studio does not start without
`--oidc-issuer` or `--no-login` (and not with both), so a missing or misspelt setting never means open access.

- **With its own login** (`--oidc-issuer`, below): a church is for its members, who join with an invite link.
- **Without** (`--no-login`): no access control. Everyone sees every church and can add churches (the desktop app, a trusted
  network, or behind a login proxy that lets only the right people through).

## Its own login (OpenID Connect)

The studio logs users in with any OpenID Connect provider (Pocket ID, Authentik, Keycloak, Google, Entra …).
[Logging in with Pocket ID](login-pocket-id.md) is the recommended setup, step by step, with other providers at
the end. Register the studio there as a client with the callback `<public-url>/auth/callback` and the logout
callback `<public-url>/auth/logout/callback` (PKCE; a client secret is optional), and start it with:

```bash
sermon-studio --data-dir /data --oidc-issuer https://id.example.org --oidc-client-id <id> \
  --oidc-client-secret-file /run/secrets/oidc --public-url https://sermons.example.org/
```

- A user is the provider's issuer and `sub`, nothing else. Name and email from the login are shown, never used
  to give access. Users are kept in `<data-dir>/users.json`, sessions (30 days, only their hashes) in `sessions.json`.
- Every page and every API call needs a session: a page goes to the login and comes back, the API answers 401.
  Health checks use `/healthz`, which is open.
- A church's members are in `<church>/members.json`, each an **admin** or a **member**. Members use the studio:
  recordings, editing, runs, downloads and shares. Admins also change the church's settings (name, logo, tags,
  processing, intro and outro clips, how long ended shares are kept) and who its members are. A user who is no
  church's member is told to ask for an invite link.
- **Settings → Members** (admins): the members with their name and email from the login, their role, Remove; invite
  links (role, 1, 7 or 30 days), shown once with a copy button; the open invites, with Revoke. A member sees only
  **Leave this church…**. A church always keeps an admin: the last one cannot step down, leave or be removed.
- **Invite links** are single use and kept only as a hash, in `<church>/invites.json`. Opening one logs in and asks
  to accept; accepting makes that login a member (an admin invite makes a member an admin).
- **Churches** are added by the site administrator, with `sermon-studio admin` (below), not in the studio.
- **Log out** ends the studio's session, and then the login at the provider too, when the provider supports it
  (OpenID Connect RP-initiated logout: an `end_session_endpoint`). The browser goes there and comes back to the
  studio's logged-out page. For that the ID token from the login is kept with the session in `sessions.json`,
  never in a cookie. A provider without it leaves its own login as it is.

## Each church at its own address

With `--church-host '{church}.sermon-studio.app'` every church has a host of its own
(`norkirken-melhus.sermon-studio.app`), and the login is on the host of `--public-url`:

```bash
sermon-studio --data-dir /data --oidc-issuer https://id.example.org --oidc-client-id <id> \
  --oidc-client-secret-file /run/secrets/oidc --public-url https://app.sermon-studio.app/ \
  --cookie-domain sermon-studio.app --church-host '{church}.sermon-studio.app'
```

- **The church is the host's**, and the user must be one of its members: asking a church's host for another
  church, or the login host for any, finds nothing. An unknown church's host says so.
- **One login for all of them**: the session cookie is set on `--cookie-domain`, which both the login host and the
  church hosts must be under (checked at start). A page on a church host goes to the login on the login host and
  comes back to where it was; the way back may only be the login host or a church host, with the same scheme and port.
- **Log out** on any host ends the session everywhere and comes back through the login host, so the provider still
  has exactly two addresses: `<public-url>/auth/callback` and `<public-url>/auth/logout/callback`.
- **The login host** serves the login, invite links (`<public-url>/invite.html#…`, which take you to the church's
  host) and the church picker (`/`, which goes straight to your church when you have one).
- The church stays in the page's address (`#/norkirken-melhus/…`); a link that names another church goes to that
  church's host.
- Church ids the site's own hosts may have are reserved: `app`, `share`, `www`, `id`, `api`, `auth`, `login`, `mail`,
  `admin`, `static`, `status`, `docs`, `help`.
- The public share page keeps its own host (`--share-listen`, `--share-url`, e.g. `share.sermon-studio.app`).

## The site administrator: `sermon-studio admin`

Run where the studio runs, with the same data dir (`--data-dir`, or `SERMON_STUDIO_DATA_DIR`, which the container
already has when it is set that way). The commands talk to the running studio over its admin socket,
`<data-dir>/.admin.sock` (only the studio's own user can use it, never the network), so a change takes effect at once,
with the same checks as in the studio. `--json` prints JSON for scripts.

```bash
sermon-studio admin church create norkirken-melhus --name "Norkirken Melhus"   # prints an invite for its first admin
sermon-studio admin church list
sermon-studio admin church rename norkirken-melhus "Norkirken"
sermon-studio admin members norkirken-melhus
sermon-studio admin invite norkirken-melhus --role admin --expires 7d   # also how to get back in when a church has lost its admins
sermon-studio admin remove norkirken-melhus <id or email>               # the id is in `admin members`
# on Kubernetes: kubectl exec deploy/sermon-studio -- sermon-studio admin church list
```

A church always keeps an admin: removing its last one is refused (invite a new admin first). Only one studio runs on a
data dir: a second one finds the first's socket and refuses to start. Without the login there are no members or
invites, and churches are also added in the studio.

Without `--oidc-issuer` the server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.
