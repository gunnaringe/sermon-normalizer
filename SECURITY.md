# Security

## Reporting a problem

Please report security issues privately, through
[GitHub's private vulnerability reporting](https://github.com/gunnaringe/sermon-studio/security/advisories/new),
not as a public issue. You will get an answer within a few days.

## What to know when you run it

- **The studio has no login of its own.** Run it on a trusted network, or behind a proxy that handles
  login. With `--groups-header` it reads the user's groups from a header the proxy sets, so every request
  must come through that proxy ([access.md](docs/access.md)).
- **The public upload page is separate on purpose** (`--share-listen`). It serves only itself and
  `api/share/*`; nothing of the studio and nothing to list or download (the one file it sends is the church's
  logo, and only to a valid code). Every request carries a share
  code (80 bits; only a hash is stored). Wrong codes and request rates are limited, uploads are checked
  to be the audio or video type they claim, and an upload is refused when the disk would get within
  1 GB of full. Run it on its own host, not behind the login proxy.
- Paths are kept inside the data folder (no `..`, no hidden names, no links out), and cross-site
  POSTs are refused.
