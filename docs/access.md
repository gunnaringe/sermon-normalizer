# Access: which churches a user sees

There are two ways to run the studio:

- **With its own login** (`--oidc-issuer`, below): a church is for its members, who join with an invite link.
- **Without**: no access control. Everyone sees every church and can add churches (the desktop app, a trusted
  network, or behind a login proxy that lets only the right people through).

## Its own login (OpenID Connect)

The studio logs users in with any OpenID Connect provider (Pocket ID, Authentik, Keycloak, Google, Entra …).
Register it there as a client with the callback `<public-url>/auth/callback` (PKCE; a client secret is
optional), and start it with:

```bash
sermon-studio --data-dir /data --oidc-issuer https://id.example.org --oidc-client-id <id> \
  --oidc-client-secret-file /run/secrets/oidc --public-url https://sermons.example.org/
```

- A user is the provider's issuer and `sub`, nothing else. Name and email from the login are shown, never used
  to give access. Users are kept in `<data-dir>/users.json`, sessions (30 days, only their hashes) in `sessions.json`.
- Every page and every API call needs a session: a page goes to the login and comes back, the API answers 401.
  Health checks use `/healthz`, which is open.
- A church's members are in `<church>/members.json`, each an admin or a member. A user who is no church's member
  is told to ask for an invite link.
- **Invite links** are made where the studio runs, and are single use (7 days by default):

  ```bash
  sermon-studio admin invite norkirken-melhus --role admin --data-dir /data --public-url https://sermons.example.org/
  # on Kubernetes: kubectl exec deploy/sermon-studio -- sermon-studio admin invite …
  ```

  Opening the link logs in and asks to accept; accepting makes that login a member (an admin invite makes a
  member an admin). Only a hash of each invite is kept, in `<church>/invites.json`. This is also how to get back in
  when a church has lost its admins.
- With the login on, churches are not added in the studio.
- **Log out** ends the studio's session. The provider's own login is left as it is: on a shared computer, log out
  there too.

Without `--oidc-issuer` the server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.
