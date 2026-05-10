const http = require('http');
const events = [];

const server = http.createServer((req, res) => {
    if (req.method === 'GET' && req.url === '/health') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ ok: true, count: events.length }));
        return;
    }
    if (req.method === 'GET' && req.url === '/dump') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(events, null, 2));
        return;
    }
    if (req.method === 'POST' && req.url === '/span-events') {
        const chunks = [];
        req.on('data', c => chunks.push(c));
        req.on('end', () => {
            try {
                const e = JSON.parse(Buffer.concat(chunks).toString());
                events.push(e);
                const dir = e.lifecycle === 'start' ? '>>' : '<<';
                const dur = e.durationMs ? (' ' + e.durationMs + 'ms') : '';
                const parent = e.parentSpanId ? (' parent=' + e.parentSpanId.slice(0, 8)) : '';
                console.log(`${dir} ${e.spanKind}:${e.spanName} [${e.status}]${dur} trace=${e.traceId.slice(0, 8)} span=${e.spanId.slice(0, 8)}${parent}`);
                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end('{"ok":true}');
            } catch (err) {
                res.writeHead(400, { 'Content-Type': 'application/json' });
                res.end('{"err":"bad json"}');
            }
        });
        return;
    }
    res.writeHead(404);
    res.end();
});

server.listen(59600, '127.0.0.1', () => {
    console.log('Tracer listening on http://127.0.0.1:59600');
});
