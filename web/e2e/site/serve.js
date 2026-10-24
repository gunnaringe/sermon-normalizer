// Serves the website (SITE, default /site) as Cloudflare's static assets do for it (site/wrangler.jsonc):
// /guide is guide.html, a folder is its index.html, an unknown path is 404.html with status 404, and the
// headers in _headers for /* (the Content-Security-Policy) go with every response. Files in .assetsignore are
// not served. For the site's test only (site.spec.js).
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

const root = process.env.SITE || '/site';
const port = Number(process.env.PORT || 8090);
const types = {
  '.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.webp': 'image/webp',
  '.mp4': 'video/mp4', '.webm': 'video/webm',
};
const ignored = new Set(fs.readFileSync(path.join(root, '.assetsignore'), 'utf8').split('\n').map((l) => l.trim()).filter((l) => l && !l.startsWith('#')));
ignored.add('_headers');

// The headers of the /* block in _headers.
const headers = {};
let inAll = false;
for (const line of fs.readFileSync(path.join(root, '_headers'), 'utf8').split('\n')) {
  if (!line.trim() || line.startsWith('#')) continue;
  if (!/^\s/.test(line)) { inAll = line.trim() === '/*'; continue; }
  if (inAll) {
    const i = line.indexOf(':');
    headers[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
}

function file(p) {
  const full = path.join(root, p);
  if (!full.startsWith(root) || ignored.has(path.basename(full))) return null;
  try {
    const st = fs.statSync(full);
    if (st.isFile()) return full;
    if (st.isDirectory()) return file(path.join(p, 'index.html'));
  } catch { /* not there */ }
  return null;
}

http.createServer((req, res) => {
  const p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
  let found = file(p) || (path.extname(p) ? null : file(p + '.html'));
  let status = 200;
  if (!found) {
    found = file('/404.html');
    status = 404;
  }
  res.writeHead(status, { ...headers, 'Content-Type': types[path.extname(found)] || 'application/octet-stream' });
  if (req.method === 'HEAD') res.end();
  else fs.createReadStream(found).pipe(res);
}).listen(port, '127.0.0.1');
