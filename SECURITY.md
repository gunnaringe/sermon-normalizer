# Security

## Reporting a problem

Please report security issues privately, through
[GitHub's private vulnerability reporting](https://github.com/gunnaringe/sermon-studio/security/advisories/new),
not as a public issue. You will get an answer within a few days.

## What to know when you run it

- **Login.** With `--oidc-issuer` the studio logs users in itself (OpenID Connect, code flow with PKCE; the ID
  token's signature, issuer, audience, expiry and nonce are checked) and a church is for its members, who join with a
  single-use invite link. Sessions and invites are random 256-bit values, stored only as hashes. Logging out
  ends the login at the provider too where it supports that (RP-initiated logout); the ID token it needs is kept
  with the session on the server, never in a cookie. Without it there is no
  access control: run the studio on a trusted network, or behind a proxy that handles login ([access.md](docs/access.md)).
- **The public share page is separate on purpose** (`--share-listen`). It serves only itself and
  `api/share/*`; nothing of the studio and nothing to list. The only files it sends are the church's logo
  to a valid code, and the finished files of the one sermon a download share gives, to its code (a counted
  download gets an address of 128 random bits that works for 6 hours). Every request carries a share
  code (80 bits; only a hash is stored). Wrong codes and request rates are limited, uploads are checked
  to be the audio or video type they claim, and an upload is refused when the disk would get within
  1 GB of full. Run it on its own host, not behind the login proxy.
- Paths are kept inside the data folder (no `..`, no hidden names, no links out), and cross-site
  POSTs are refused.
