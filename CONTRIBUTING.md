# Contributing

Thanks for helping. The code is Go (standard library only; ffmpeg is run as a command) with a
plain-JS frontend: no build step, no framework.

## Checks

```bash
scripts/test.sh            # unit tests, seconds: run after every change
scripts/test.sh all        # vet + all tests with real ffmpeg (container)
scripts/test.sh e2e        # browser tests (Playwright, container); after changing web/static
scripts/test.sh image      # the Docker build; before release changes
```

The integration tests run the whole pipeline with real ffmpeg on synthetic recordings;
`docker build .` runs them too. `ffmpeg` is not needed on the host: `all` and `e2e` use podman or docker.

## Pull requests

Every change goes in a pull request, never straight to `main`: merging publishes a new image.
A change to anything visible includes screenshots (`scripts/pr-shots.sh`, see [AGENTS.md](AGENTS.md)).

## Conventions

- All UI text goes through `t('English text')`, and needs a Norwegian bokmål and nynorsk entry in
  `web/static/locales.js` (`TestLocales` fails otherwise).
- The public upload page is on the internet without a login: add nothing to it that reads files or
  reaches the studio (`TestSharePublicIsSeparate` checks this).
- Outputs are written to a `.partial` file and renamed; a source is deleted only after its copy is verified.

The code map and the rest of the rules are in [AGENTS.md](AGENTS.md), written for coding agents but
useful to anyone. Background and design decisions: [docs/design.md](docs/design.md).
