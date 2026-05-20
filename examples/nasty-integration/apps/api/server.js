import http from 'node:http';

const server = http.createServer((request, response) => {
  if (request.url === '/health') {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ status: 'ok' }));
    return;
  }
  response.writeHead(200, { 'content-type': 'text/plain' });
  response.end('nasty api fixture');
});

server.listen(3100, '0.0.0.0');
