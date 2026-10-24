# Running it

The full reference: build, Docker, the data folder, flags. The short version is at the bottom of the [README](../README.md).

## Requirements

- `ffmpeg` in PATH (Linux: `sudo apt install ffmpeg`), or use the Docker image
- Go 1.25+ to build

## Build

```bash
go build -o sermon-studio ./cmd/sermon-studio
```

Checks and tests are in [CONTRIBUTING.md](../CONTRIBUTING.md).

# Docker (ffmpeg included)

ffmpeg is a separate C program and cannot be built into the Go binary, so the image packages both.

```bash
docker build -t sermon-studio .
docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" \
  -v /path/to/data:/data -v /path/to/cache:/cache \
  sermon-studio --no-login --data-dir /data --cache-dir /cache
```

`-u` makes the created files owned by your user, not root.
The build runs `go vet` and `go test`, and checks that ffmpeg has libopus and libmp3lame.

# Running

```bash
sermon-studio --data-dir /path/to/data --listen :8080 --no-login                 # no login: a trusted network
sermon-studio --data-dir /path/to/data --listen :8080 --oidc-issuer https://… …  # the studio's own login
```

The login is always chosen: the studio does not start without one of `--oidc-issuer` and `--no-login`
([access](access.md)). `sermon-studio --help` lists every flag; `sermon-studio serve` is the same as no command.

**Environment variables:** every flag can also be set as `SERMON_STUDIO_<FLAG>`, in capitals with `_` for `-`
(`SERMON_STUDIO_DATA_DIR=/data`, `SERMON_STUDIO_NO_LOGIN=true`), e.g. from a Kubernetes ConfigMap with `envFrom`. A
flag wins over its variable. A `SERMON_STUDIO_*` variable this version does not know is logged as a warning (with the
nearest name it does know) and otherwise ignored, so a setting can be added before the version that reads it.

The data dir has a folder per church (a church is a folder with a `Taler` folder in it):

```
data/norkirken-melhus/
  Taler/<year>/<date>[ <name>]/   the sermons
  Innboks/                        the inbox
  church.json                     optional: name, default tags (written by the app)
```

The folder name is the church's id and, until it is renamed on the Settings page, its name. Add a
church in the app ("+ Add church…") or just make the folder. The church is the first part of every address
(`#/<church>` the recordings, `#/<church>/folder/<path>` the editor, `#/<church>/settings` the settings), so a link opens
that church whatever the browser used last. The ids `folder`, `folders`, `recording`, `recordings`, `settings`, `upload` and
`shares` are reserved for that. A `church.json` that cannot be read stops
the server from starting rather than being overwritten.

**Logo:** *Settings → Church → Upload logo…* (PNG, JPEG, WebP or SVG, up to 1 MB) puts the church's own
logo in the header instead of the app's; it is kept as `logo.<ext>` in the church folder. A user with one
church and no right to add more sees just its name in the header; otherwise a quiet dropdown switches church.

| Flag | Default | |
|---|---|---|
| `--data-dir` | | the folder with a folder per church (see above) |
| `--root`, `--inbox` | | one church without a church folder, instead of `--data-dir`: its date folders and the inbox where new recordings arrive. Settings are not saved. |
| `--oidc-issuer` | | the OpenID Connect provider: the studio's own login; a church is for its members, who join with an invite link ([access](access.md)). This or `--no-login` is required |
| `--no-login` | | no login and no access control: anyone who reaches the studio can use every church (the desktop, a trusted network). This or `--oidc-issuer` is required |
| `--oidc-client-id`, `--oidc-client-secret-file` | | the studio's client at the provider; no secret file = a public client (PKCE) |
| `--public-url` | | with `--oidc-issuer`: the studio's address as the browser sees it (the callback is `<public-url>/auth/callback`) |
| `--cookie-domain` | | with `--oidc-issuer`: a parent domain for the session cookie, shared by the studio's hosts |
| `--listen` | `:8080` | address to listen on |
| `--cache-dir` | user cache folder | waveform data and listening previews |
| `--cache-days` | 30 | remove cached analyses not used for this long |
| `--parallel-runs` | 1 | how many runs (making the files) work at once, across all churches; each uses about one core, more wait their turn. Two lets a sample be heard while a run is working |
| `--trash-days` | 30 | how long a deleted recording stays in the trash (Settings → Trash) before it is removed for good |
| `--layout` | `2006/2006-01-02` | where new recordings go under a church's `Taler`, as a Go time layout |
| `--ffmpeg` | `ffmpeg` | path to ffmpeg |
| `--share-listen` | | address of the public share page, for sending recordings and downloading sermons (see [Shares](guide.md#shares)); unset = no shares |
| `--share-url` | | the share page's public address, so the studio can show share links |
| `--share-ip-header` | | header with the client's address, for the rate limits (Cloudflare: `CF-Connecting-IP`) |
| `--share-fails-per-ip`, `--share-fails-total` | 5, 30 | wrong codes in 10 minutes before an address is blocked for an hour / codes are refused for everyone for 10 minutes |
| `--share-requests-per-ip`, `--share-requests-total` | 120, 1200 | requests per minute to the share page |
| `--share-uploads-per-ip`, `--share-uploads-total` | 3, 10 | chunks received at the same time |

Churches: the dropdown in the header switches church. A request names its church with
`?tenant=<id>` or `X-Tenant`; without one it is the first church the user sees.

Access control, for several users: [access.md](access.md); the login with Pocket ID, step by step: [login-pocket-id.md](login-pocket-id.md).
