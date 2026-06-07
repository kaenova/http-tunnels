const WebSocket = require('ws');

const target = process.argv[2];
if (!target) {
  console.error('usage: node ws-client.js <ws-url>');
  process.exit(2);
}

const ws = new WebSocket(target, {
  headers: {
    Host: process.argv[3] || undefined,
  },
});

let done = false;
function finish(code) {
  if (done) return;
  done = true;
  setTimeout(() => process.exit(code), 50);
}

ws.on('open', () => {
  console.log('OPEN');
  ws.send('hello');
});

ws.on('message', (data, isBinary) => {
  const text = isBinary ? data.toString('hex') : data.toString();
  console.log('MESSAGE', text);
  if (text.includes('echo:hello')) {
    ws.close(1000, 'done');
  }
});

ws.on('close', (code, reason) => {
  console.log('CLOSE', code, reason.toString());
  finish(0);
});

ws.on('error', (err) => {
  console.error('ERROR', err.message);
  finish(1);
});
