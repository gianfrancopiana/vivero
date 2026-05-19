const http = require('node:http');
const { URL } = require('node:url');

const port = Number(process.env.PORT || 3000);
const startedAt = new Date().toISOString();

function html(title, body) {
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>${title} · Vivero Agent Demo</title>
  <style>
    :root { color-scheme: light dark; font-family: Inter, ui-sans-serif, system-ui, sans-serif; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    main { max-width: 880px; margin: 0 auto; padding: 48px 24px; }
    nav { display: flex; gap: 16px; margin: 20px 0 32px; }
    a { color: #2563eb; }
    .card { border: 1px solid color-mix(in oklab, CanvasText 18%, transparent); border-radius: 18px; padding: 24px; box-shadow: 0 20px 60px color-mix(in oklab, CanvasText 8%, transparent); }
    code { background: color-mix(in oklab, CanvasText 10%, transparent); border-radius: 6px; padding: 2px 6px; }
    .status { display: inline-flex; align-items: center; gap: 8px; font-weight: 700; }
    .dot { width: 10px; height: 10px; border-radius: 999px; background: #16a34a; display: inline-block; }
  </style>
</head>
<body>
  <main>
    <p class="status"><span class="dot"></span> healthy preview</p>
    <h1>${title}</h1>
    <nav aria-label="Demo pages">
      <a href="/">Home</a>
      <a href="/products">Products</a>
      <a href="/settings">Settings</a>
      <a href="/api/status">Status JSON</a>
    </nav>
    <section class="card">${body}</section>
  </main>
</body>
</html>`;
}

function isAuthenticated(req) {
  return /(?:^|;\s*)demo_session=agent/.test(req.headers.cookie || '');
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  if (url.pathname === '/health') {
    res.writeHead(200, { 'content-type': 'text/plain; charset=utf-8' });
    res.end('ok');
    return;
  }
  if (url.pathname === '/api/status') {
    res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({ ok: true, app: 'agent-demo', startedAt }));
    return;
  }
  if (url.pathname === '/products') {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(html('Products', '<p>Demo product catalog loaded.</p><button data-testid="buy-button">Preview checkout</button>'));
    return;
  }
  if (url.pathname === '/settings') {
    if (!isAuthenticated(req)) {
      res.writeHead(401, { 'content-type': 'text/html; charset=utf-8' });
      res.end(html('Sign in required', '<p>Settings requires the committed non-secret Playwright storage-state fixture.</p>'));
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(html('Settings', '<p>Signed in as <strong>demo-agent@example.test</strong>.</p><label>Display name <input value="Demo Agent" /></label>'));
    return;
  }
  if (url.pathname === '/') {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(html('Agent-ready local preview', '<p>This tiny app is a fixture for Vivero install, preview, QA, final proof, diagnosis, and teardown smoke tests.</p><p>It has no external dependencies beyond the runtime image.</p>'));
    return;
  }
  res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
  res.end('not found');
});

server.listen(port, '0.0.0.0', () => {
  console.log(`agent-demo listening on ${port}`);
});
