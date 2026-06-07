const http = require('http');
const { WebSocketServer } = require('ws');

const port = Number(process.env.PORT || 18080);

const server = http.createServer((req, res) => {
  if (req.url === '/health') {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ ok: true, service: 'ws-mock', port }));
    return;
  }
  res.writeHead(200, { 'content-type': 'text/plain' });
  res.end('ws-mock-server');
});

const wss = new WebSocketServer({ noServer: true });

wss.on('connection', (ws, req) => {
  ws.send(JSON.stringify({ type: 'welcome', path: req.url }));
  ws.on('message', (msg, isBinary) => {
    if (isBinary) {
      ws.send(Buffer.concat([Buffer.from('echo:'), Buffer.from(msg)]), { binary: true });
      return;
    }
    ws.send(`echo:${msg.toString()}`);
  });
  ws.on('ping', (data) => ws.pong(data));
});

server.on('upgrade', (req, socket, head) => {
  if (!req.url.startsWith('/ws')) {
    socket.write('HTTP/1.1 404 Not Found\r\n\r\n');
    socket.destroy();
    return;
  }
  wss.handleUpgrade(req, socket, head, (ws) => {
    wss.emit('connection', ws, req);
  });
});

server.listen(port, '127.0.0.1', () => {
  console.log(`ws-mock listening on http://127.0.0.1:${port}`);
});
