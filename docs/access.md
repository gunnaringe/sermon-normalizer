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
  to accept; accepting makes that login a member (an admin invite makes a member an admin). They can also be made
  where the studio runs, which is how to get back in when a church has lost its admins:

  ```bash
  sermon-studio admin invite norkirken-melhus --role admin --data-dir /data --public-url https://sermons.example.org/
  # on Kubernetes: kubectl exec deploy/sermon-studio -- sermon-studio admin invite …
  ```

- With the login on, churches are not added in the studio.
- **Log out** ends the studio's session, and then the login at the provider too, when the provider supports it
  (OpenID Connect RP-initiated logout: an `end_session_endpoint`). The browser goes there and comes back to the
  studio's logged-out page. For that the ID token from the login is kept with the session in `sessions.json`,
  never in a cookie. A provider without it leaves its own login as it is.

Without `--oidc-issuer` the server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.
