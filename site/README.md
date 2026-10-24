# The website: sermon-studio.app

What Sermon Studio is, how it works, the guide in short, privacy, and the desktop app's download, in English,
bokmål and nynorsk. Plain HTML and CSS with a little JavaScript (`site.js`), no build step. It loads nothing
from other hosts (`_headers` sets a Content-Security-Policy that keeps it so).

| | |
|---|---|
| `index.html`, `guide.html`, `privacy.html`, `404.html` | the pages: the markup, with `data-t="key"` where a text goes |
| `lang/en.json`, `nb.json`, `nn.json` | all the texts, one file per language, with the same keys |
| `site.js` | the language (`?lang=`, else the one chosen before, else the browser's), the selector, the pictures, Log in |
| `style.css` | the studio's look: its green, its gradient, light and dark as the device is |
| `img/logo.svg`, `img/*.png` | the studio's logo and icons, copied from `web/static/icons/` |
| `img/shots/<lang>/*.webp` | the studio in each language, made by `scripts/site-shots.sh shots` |
| `media/demo.mp4`, `demo.webm`, `demo-poster.webp` | the video, in English, made by `scripts/site-shots.sh video` |

**Log in** is hidden until the hosted studio runs (#82, #84): set `SHOW_LOGIN = true` in `site.js` then. Its
address is `LOGIN_URL` beside it.

## Changing it

- A new text: add its key to all three files in `lang/`. A new picture of the studio: add it to
  `web/e2e/site-shots/shots.spec.js` and run `scripts/site-shots.sh shots`.
- When the studio looks different: `scripts/site-shots.sh` makes all the pictures and the video again (about 10
  minutes, in podman; the speech comes from `scripts/fetch-sample.sh`).
- Check it: `scripts/test.sh site` serves this folder and checks every page in every language, the selector,
  that the languages have the same keys, every link and picture, and the video. It saves screenshots of the
  pages in `web/e2e/screenshots/site-*.png` for the PR.

## Deploying

A Cloudflare Worker with static assets only, `sermon-studio-site` (`wrangler.jsonc`), deployed by Workers
Builds on every push to `main` that changes `site/`. Set up once, in the Cloudflare dashboard:

1. **Workers & Pages → Create → Import a repository**: `gunnaringe/sermon-studio`.
2. Project name `sermon-studio-site` (as in `wrangler.jsonc`). **Root directory** `site`. No build command.
   Deploy command `npx wrangler deploy` (the default).
3. In the Worker's **Settings → Build**: production branch `main`, and **Build watch paths**: include `site/**`,
   so changes to the studio itself do not deploy the site.
4. **Settings → Domains & Routes → Add → Custom domain**: `sermon-studio.app`, then `www.sermon-studio.app`
   (the zone must be on the same Cloudflare account). Both show the site. To send www to the bare domain
   instead, add a redirect rule for `www.sermon-studio.app/*` → `https://sermon-studio.app/${1}` (301).

`.assetsignore` keeps `wrangler.jsonc`, this README and wrangler's own files from being served.
