// Stand-in for an AMR Health server, for testing the stored-scan reader without a sign-in to a real one.
// It serves REAL laser replies: the samples of a recording in the workspace, each as one "stored scan"
// with the position the robot reported in that sample.
//   node tests\health_standin.js <port> <workspace>\drives\<recording> [every-nth-sample]
// Sign-in: user "tester", password "tester". Endpoints as in AMR Health 0.7.74:
//   POST /api/auth/login   GET /api/amr/laser-scans   GET /api/amr/laser-scans/<id>
const http = require('http'), fs = require('fs'), path = require('path');
const [port, dir, nth = '5'] = process.argv.slice(2);
const TOKEN = 'standin-' + Date.now();
const ids = fs.readdirSync(dir).map(f => (f.match(/^loc_(\d+)\.json$/) || [])[1]).filter(Boolean).map(Number).sort((a, b) => a - b).filter((v, i) => i % +nth === 0);
const pad = i => String(i).padStart(4, '0');
const t0 = Date.now() - 6 * 3600e3;
const scans = ids.map((i, k) => {
  const loc = JSON.parse(fs.readFileSync(path.join(dir, 'loc_' + pad(i) + '.json'), 'utf8'));
  const at = new Date(t0 + k * 60e3).toISOString();
  return { id: i, plant: 'Test', amr: 'AMR-0' + (1 + k % 3), captured_at: at, status_at: at, reason: loc.confidence < 0.5 ? 'confidence' : 'sample', detail: '', confidence: loc.confidence, pos_x: loc.x, pos_y: loc.y, angle: loc.angle, station: loc.current_station || '', reloc_status: '', lasers: 2, points: 700, reply_has_points: true, raw_bytes: 35000, stored_bytes: 5000, sha256: '' };
});
http.createServer((req, res) => {
  const u = new URL(req.url, 'http://x'), send = (code, obj) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(typeof obj === 'string' ? obj : JSON.stringify(obj)); };
  if (req.method === 'POST' && u.pathname === '/api/auth/login') {
    let b = ''; req.on('data', d => b += d); req.on('end', () => { let q = {}; try { q = JSON.parse(b); } catch (e) { } q.username === 'tester' && q.password === 'tester' ? send(200, { token: TOKEN, username: 'tester' }) : send(401, { error: 'invalid username or password' }); });
    return;
  }
  if (u.pathname.startsWith('/api/amr/laser-scans')) {
    if (req.headers.authorization !== 'Bearer ' + TOKEN) return send(401, { error: 'missing authorization token' });
    const id = u.pathname.split('/')[4];
    if (id) { const f = path.join(dir, 'laser_' + pad(+id) + '.json'); return fs.existsSync(f) ? send(200, fs.readFileSync(f, 'utf8')) : send(404, { error: 'no such scan' }); }
    const amr = u.searchParams.get('amr');
    return send(200, { plant: 'Test', scans: scans.filter(s => !amr || s.amr === amr).reverse(), note: '' });
  }
  send(404, { error: 'not found' });
}).listen(+port, '127.0.0.1', () => console.log('stand-in AMR Health on http://127.0.0.1:' + port + ' with ' + scans.length + ' scans'));
