const http = require('http');
const fs = require('fs');

const port = Number(process.env.PORT || 3000);
const backingURL = process.env.BACKING_URL || 'http://api:3101';
const seedPath = process.env.SEED_PATH || '/data/seed.txt';

function readSeed() {
  try {
    return fs.readFileSync(seedPath, 'utf8').trim();
  } catch (_err) {
    return 'missing-seed';
  }
}

async function backingStatus() {
  try {
    const response = await fetch(`${backingURL}/health`);
    if (!response.ok) return `backing-http-${response.status}`;
    return (await response.text()).trim();
  } catch (err) {
    return `backing-error:${err.message}`;
  }
}

const server = http.createServer(async (req, res) => {
  if (req.url === '/health') {
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.end('ok');
    return;
  }

  if (req.url === '/api/status') {
    const status = {
      app: 'integration-stack',
      seed: readSeed(),
      backing: await backingStatus(),
    };
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(status));
    return;
  }

  res.writeHead(200, { 'content-type': 'text/html' });
  res.end(`<!doctype html>
<title>Vivero integration fixture</title>
<h1>Integration fixture ready</h1>
<p>seed=${readSeed()}</p>
<p>backing=${await backingStatus()}</p>`);
});

server.listen(port, '0.0.0.0', () => {
  console.log(`integration fixture listening on ${port}`);
});
