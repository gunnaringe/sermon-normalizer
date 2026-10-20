# Access: which churches a user sees

Without `--groups-header` there is no access control: everyone sees every church and can add
churches. With `--groups-header X-Forwarded-Groups` the app reads the user's groups (space-separated)
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

The server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.
