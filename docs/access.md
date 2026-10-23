# Access: which churches a user sees

There are three ways to run the studio:

- **Its own login** (`--oidc-issuer`, below): a church is for its members, who join with an invite link.
- **Behind a login proxy** (`--groups-header`, further down): a church is for a group the proxy sends.
- **Neither**: no access control. Everyone sees every church and can add churches (the desktop app, a trusted network).

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

## Behind a login proxy

With `--groups-header X-Forwarded-Groups` the app reads the user's groups (space-separated)
from that header, which the login proxy must set, and nothing else:

- a church `<id>` is for the members of the group **`sermon_studio_<id>`**, with underscores for the hyphens in the id
  (`norkirken-melhus` → `sermon_studio_norkirken_melhus`, like Pocket ID names groups); two churches
  that would get the same group (`a-b`, `a_b`) are refused
- members of **`sermon_studio_admin`** see every church and are the only ones who can add churches
  (the app tells them the group to create)
- users in no matching group get a 403 page; a church they do not see answers as if it did not exist
- **`admin`** (and `default`) cannot be a church id, so no church's group can ever be the
  administrators' group: the name is refused when adding, and a church folder with that name stops
  the server from starting

The header is only trustworthy when every request comes through the proxy and the proxy replaces it
(Traefik removes the headers named in `authResponseHeaders` before copying the auth response's).
Clients that reach the app directly have no groups and get nothing.

Without `--oidc-issuer` the server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.
