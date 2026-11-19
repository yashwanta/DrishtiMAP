'use strict';
const $ = s => document.querySelector(s), $$ = s => [...document.querySelectorAll(s)];
const el = (tag, props = {}, ...kids) => { const e = Object.assign(document.createElement(tag), props); for (const k of kids) e.append(k); return e; };

async function api(path, opts = {}) {
  const r = await fetch(path, opts);
  let j = null; try { j = await r.json(); } catch (e) { }
  if (!r.ok) throw new Error((j && j.error) || ('request failed (' + r.status + ')'));
  return j;
}
const post = (path, body) => api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
const f32 = b64 => { if (!b64) return new Float32Array(0); const s = atob(b64), u = new Uint8Array(s.length); for (let i = 0; i < s.length; i++) u[i] = s.charCodeAt(i); return new Float32Array(u.buffer); };

const S = {
  ws: { maps: [], candidates: [], drives: [], robots: [] }, state: null,
  A: null, B: null,            // shown maps: {name, v}
  drive: '', ov: {},           // overlays: cloud, rows, track, jumps, hi (highlighted lines)
  draw: { mode: '', poly: [], box: null },
  view: { cx: 0, cy: 0, k: 8 }, layers: {}, job: null,
};

// ---------------------------------------------------------------- output + jobs
const out = (title, text) => { $('#outTitle').textContent = title; $('#out').textContent = text; $('#out').scrollTop = 0; };
async function run(tool, args, quiet) {
  const { job } = await post('/api/run', { tool, args });
  S.job = job;
  if (!quiet) { $('#progWrap').classList.remove('hidden'); $('#outTitle').textContent = tool + ' …'; }
  try {
    for (;;) {
      await new Promise(r => setTimeout(r, 250));
      const j = await api('/api/job?id=' + job + '&brief=1');
      if (!quiet) { $('#prog').value = j.progress || 0; $('#progNote').textContent = (j.note || '') + '  ' + Math.round(j.seconds) + ' s'; }
      if (j.state === 'running') continue;
      if (j.state !== 'done') throw new Error(j.error || j.state);
      return (await api('/api/job?id=' + job)).result;
    }
  } finally { if (!quiet) $('#progWrap').classList.add('hidden'); S.job = null; }
}
$('#stopJob').onclick = () => S.job && post('/api/cancel', { id: S.job });
$('#saveOut').onclick = async () => {
  const name = ($('#outTitle').textContent.replace(/[^A-Za-z0-9 _.-]/g, '').trim() || 'output') + ' ' + stamp() + '.txt';
  try { const r = await api('/api/save?name=' + encodeURIComponent(name), { method: 'POST', body: $('#out').textContent.replace(/\n/g, '\r\n') }); toast('Saved ' + r.file); } catch (e) { toast(e.message); }
};
const stamp = () => { const d = new Date(), p = n => String(n).padStart(2, '0'); return d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate()) + '-' + p(d.getHours()) + p(d.getMinutes()) + p(d.getSeconds()); };
let toastT; const toast = t => { $('#coords').textContent = t; clearTimeout(toastT); toastT = setTimeout(() => { $('#coords').textContent = ''; }, 6000); };

// ---------------------------------------------------------------- workspace lists
async function refresh() {
  S.state = await api('/api/state');
  $('#wsBtn').textContent = 'Workspace: ' + S.state.workspace;
  const r = await run('list_workspace', {}, true);
  S.ws = r.data;
  for (const k of ['maps', 'candidates', 'drives', 'robots']) S.ws[k] = S.ws[k] || [];
  const mk = (ul, items, fn) => { ul.replaceChildren(...items.map(fn)); if (!items.length) ul.append(el('li', { style: 'cursor:default;color:var(--dim)' }, 'none')); };
  const mapLi = m => {
    const li = el('li', { title: m.name + '\n' + m.sizeMB + ' MB, ' + m.modified + '\nclick: show   shift-click: overlay as second map' }, el('span', { className: 'nm', textContent: m.name }));
    if (S.A && S.A.name === m.name) li.classList.add('on'); if (S.B && S.B.name === m.name) li.classList.add('on2');
    li.onclick = e => showMap(m.name, e.shiftKey);
    if (m.folder === 'candidates') {
      const d = el('button', { className: 'mini', textContent: '×', title: 'Delete this candidate file' });
      d.onclick = async e => { e.stopPropagation(); if (confirm('Delete candidate ' + m.name + '?')) { await post('/api/delete-candidate', { name: m.name }); if (S.A && S.A.name === m.name) S.A = null; if (S.B && S.B.name === m.name) S.B = null; refresh(); draw(); } };
      li.append(el('span', { className: 'acts' }, d));
    }
    return li;
  };
  mk($('#mapsList'), S.ws.maps, mapLi); mk($('#candList'), S.ws.candidates, mapLi);
  mk($('#driveList'), S.ws.drives, d => {
    const li = el('li', { title: d.name }, el('span', { className: 'nm', textContent: d.name }), el('small', { textContent: d.samples }));
    if (S.drive === d.name) li.classList.add('on');
    li.onclick = () => { S.drive = d.name; $('#selDrive').value = d.name; refreshMarks(); go('drive_summary'); };
    return li;
  });
  mk($('#robotList'), S.ws.robots, r => {
    const b = (t, tip, fn) => { const x = el('button', { className: 'mini', textContent: t, title: tip }); x.onclick = e => { e.stopPropagation(); fn(); }; return x; };
    return el('li', { title: r.ip, onclick: () => robotStatus(r.name) }, el('span', { className: 'nm', textContent: r.name }), el('small', { textContent: r.ip }),
      el('span', { className: 'acts' }, b('map', 'Copy the robot\'s current map into this workspace (read-only)', () => robotMap(r.name)), b('rec', 'Record position and lasers (read-only)', () => recordDlg(r.name)), b('×', 'Remove from list', () => saveRobots(S.ws.robots.filter(x => x.name !== r.name)))));
  });
  const all = [...S.ws.maps, ...S.ws.candidates].map(m => m.name);
  const fill = (sel, items, blank) => { const v = sel.value; sel.replaceChildren(...(blank ? [el('option', { value: '', textContent: '—' })] : []), ...items.map(n => el('option', { value: n, textContent: n }))); if (items.includes(v) || (blank && v === '')) sel.value = v; };
  fill($('#selMap'), all); fill($('#selMap2'), all, true); fill($('#selAnchor'), all, true); fill($('#selDrive'), S.ws.drives.map(d => d.name)); fill($('#scanRobot'), S.ws.robots.map(r => r.name));
  if (S.drive) $('#selDrive').value = S.drive; else S.drive = $('#selDrive').value;
  const ps = $('#selProv'); ps.replaceChildren(...S.state.providers.map(p => el('option', { value: p.id, textContent: p.name + (p.model ? ' · ' + p.model : '') })));
  ps.value = S.state.active; provNote();
}
function refreshMarks() { for (const li of $$('#driveList li')) li.classList.toggle('on', li.querySelector('.nm') && li.querySelector('.nm').textContent === S.drive); for (const li of $$('#mapsList li,#candList li')) { const n = li.querySelector('.nm'); if (!n) continue; li.classList.toggle('on', !!S.A && S.A.name === n.textContent); li.classList.toggle('on2', !!S.B && S.B.name === n.textContent); } }
function provNote() { const p = (S.state.providers || []).find(x => x.id === $('#selProv').value); $('#localNote').textContent = p && /127\.0\.0\.1|localhost/.test(p.baseUrl) ? '. This model runs on this PC; nothing leaves it' : ''; }

const prepView = v => { v.W = f32(v.walls); v.L = f32(v.lines); v.R = f32(v.rssi); return v; };
async function loadView(name) { return { name, v: prepView(await api('/api/mapview?map=' + encodeURIComponent(name))) }; }
async function showMap(name, second) {
  if (S.ed && !second) { if (name === S.ed.base) return; if (!confirm('Discard the edits on ' + S.ed.base + ' and show ' + name + '?')) { $('#selMap').value = S.ed.base; return; } await edEnd(); }
  try {
    toast('loading ' + name + ' …');
    const m = await loadView(name);
    if (second) { S.B = (S.B && S.B.name === name) ? null : m; $('#selMap2').value = S.B ? name : ''; }
    else { const first = !S.A; S.A = m; $('#selMap').value = name; S.ov = {}; if (first) fit(); }
    toast(''); refreshMarks(); draw();
  } catch (e) { toast(e.message); }
}
$('#selMap').onchange = () => showMap($('#selMap').value);
$('#selMap2').onchange = async () => { const n = $('#selMap2').value; S.B = n ? await loadView(n) : null; refreshMarks(); draw(); };
$('#selDrive').onchange = () => { S.drive = $('#selDrive').value; refreshMarks(); };

// ---------------------------------------------------------------- canvas
const cv = $('#cv'), ctx = cv.getContext('2d');
const X = x => (x - S.view.cx) * S.view.k + cv.width / 2, Y = y => cv.height / 2 - (y - S.view.cy) * S.view.k;
const wx = px => (px - cv.width / 2) / S.view.k + S.view.cx, wy = py => (cv.height / 2 - py) / S.view.k + S.view.cy;
function fit() { const m = S.A || S.B; if (!m) return; const b = m.v.bounds; S.view.cx = (b[0] + b[2]) / 2; S.view.cy = (b[1] + b[3]) / 2; S.view.k = Math.max(0.5, Math.min(cv.width / (b[2] - b[0] + 4), cv.height / (b[3] - b[1] + 4))); draw(); }
let raf = 0; const draw = () => { if (!raf) raf = requestAnimationFrame(() => { raf = 0; paint(); }); };
function dots(a, color, size) { ctx.fillStyle = color; const w = cv.width, h = cv.height, k = S.view.k, ox = w / 2 - S.view.cx * k, oy = h / 2 + S.view.cy * k; for (let i = 0; i < a.length; i += 2) { const x = a[i] * k + ox, y = oy - a[i + 1] * k; if (x >= 0 && y >= 0 && x < w && y < h) ctx.fillRect(x, y, size, size); } }
function paint() {
  const r = cv.getBoundingClientRect(), dpr = window.devicePixelRatio || 1;
  if (cv.width !== Math.round(r.width * dpr) || cv.height !== Math.round(r.height * dpr)) { cv.width = Math.round(r.width * dpr); cv.height = Math.round(r.height * dpr); }
  const L = S.layers, k = S.view.k, dp = Math.max(1, dpr);
  ctx.fillStyle = '#0e1014'; ctx.fillRect(0, 0, cv.width, cv.height);
  // grid: 5 m (10 m when zoomed out)
  const g = k < 4 ? 10 : 5; ctx.lineWidth = 1; ctx.font = 11 * dp + 'px Consolas'; ctx.fillStyle = '#4b5563';
  for (let x = Math.ceil(wx(0) / g) * g; x < wx(cv.width); x += g) { ctx.strokeStyle = x % 50 === 0 ? '#2a303a' : '#1a1e25'; ctx.beginPath(); ctx.moveTo(X(x), 0); ctx.lineTo(X(x), cv.height); ctx.stroke(); if (x % (g * 2) === 0) ctx.fillText(x, X(x) + 3, cv.height - 4); }
  for (let y = Math.ceil(wy(cv.height) / g) * g; y < wy(0); y += g) { ctx.strokeStyle = y % 50 === 0 ? '#2a303a' : '#1a1e25'; ctx.beginPath(); ctx.moveTo(0, Y(y)); ctx.lineTo(cv.width, Y(y)); ctx.stroke(); if (y % (g * 2) === 0) ctx.fillText(y, 3, Y(y) - 3); }
  const sz = Math.max(1, Math.min(4, k / 14)) * dp;
  for (const [m, wc, pc, sc] of [[S.B, '#3b82f6', '#1e3a8a', '#60a5fa'], [S.A, '#c9ced6', '#3f6b4f', '#f5a623']]) {
    if (!m) continue; const v = m.v;
    if (L.areas) for (const a of v.areas || []) { ctx.strokeStyle = '#a78bfa'; ctx.fillStyle = '#a78bfa22'; ctx.beginPath(); for (let i = 0; i < a.pts.length; i += 2) ctx[i ? 'lineTo' : 'moveTo'](X(a.pts[i]), Y(a.pts[i + 1])); ctx.closePath(); ctx.fill(); ctx.stroke(); ctx.fillStyle = '#a78bfa'; ctx.fillText(a.name, X(a.pts[0]) + 4, Y(a.pts[1]) - 4); }
    if (L.walls) dots(v.W, wc, sz);
    if (L.refl) { dots(v.R, '#22d3ee', sz); ctx.fillStyle = '#22d3ee'; for (let i = 0; i < (v.reflectors || []).length; i += 2) { ctx.beginPath(); ctx.arc(X(v.reflectors[i]), Y(v.reflectors[i + 1]), 4 * dp, 0, 7); ctx.fill(); } }
    if (L.lines) { ctx.strokeStyle = m === S.A ? '#e879f9' : '#7c3aed'; ctx.lineWidth = 1.5 * dp; ctx.beginPath(); for (let i = 0; i < v.L.length; i += 4) { ctx.moveTo(X(v.L[i]), Y(v.L[i + 1])); ctx.lineTo(X(v.L[i + 2]), Y(v.L[i + 3])); } ctx.stroke(); }
    if (L.paths) { ctx.strokeStyle = pc; ctx.lineWidth = Math.max(1, k / 12) * dp; ctx.beginPath(); for (const p of v.paths || []) { ctx.moveTo(X(p[0]), Y(p[1])); ctx.bezierCurveTo(X(p[2]), Y(p[3]), X(p[4]), Y(p[5]), X(p[6]), Y(p[7])); } ctx.stroke(); }
    if (L.stations) { ctx.fillStyle = sc; for (const s of v.stations || []) { const x = X(s.x), y = Y(s.y); if (x < -20 || y < -20 || x > cv.width + 20 || y > cv.height + 20) continue; ctx.beginPath(); ctx.arc(x, y, Math.max(2, Math.min(6, k / 6)) * dp, 0, 7); ctx.fill(); if (L.names && k > 9) ctx.fillText(s.n, x + 6 * dp, y - 5 * dp); } }
  }
  const o = S.ov;
  if (L.lasers && o.cloud) dots(o.cloud, '#ef444488', Math.max(1, sz * 0.8));
  if (L.track && o.track) for (const p of o.track) { const c = p.m === 1 ? '#22d3ee' : p.c >= 0.7 ? '#4ade80' : p.c >= 0.5 ? '#facc15' : '#f87171'; ctx.fillStyle = c; ctx.fillRect(X(p.x) - 2 * dp, Y(p.y) - 2 * dp, 4 * dp, 4 * dp); }
  if (L.track && o.rows) { for (const p of o.rows) { ctx.fillStyle = p.fit >= 55 ? '#4ade80' : p.fit >= 40 ? '#facc15' : '#f87171'; ctx.fillRect(X(p.x) - 2 * dp, Y(p.y) - 2 * dp, 4 * dp, 4 * dp); } }
  if (o.jumps) for (const j of o.jumps) { const big = j.Step >= 0.5 && !j.Ambiguous && !j.TrackBreak; ctx.strokeStyle = big ? '#ff3b3b' : '#facc15'; ctx.lineWidth = 2 * dp; ctx.beginPath(); ctx.arc(X(j.X), Y(j.Y), (big ? 14 : 9) * dp, 0, 7); ctx.stroke(); const t = (big ? 'JUMP ' : 'step ') + j.Step.toFixed(2) + ' m' + (j.TrackBreak ? ' (track restart)' : j.Ambiguous ? ' (ambiguous)' : '') + ' ' + j.Near, tw = ctx.measureText(t).width; ctx.fillStyle = '#0e1014dd'; ctx.fillRect(X(j.X) + 14 * dp, Y(j.Y) - 9 * dp, tw + 8 * dp, 17 * dp); ctx.fillStyle = ctx.strokeStyle; ctx.fillText(t, X(j.X) + 18 * dp, Y(j.Y) + 4 * dp); }
  if (o.hi) { ctx.lineWidth = 4 * dp; for (const h of o.hi) { ctx.strokeStyle = h.c; ctx.beginPath(); ctx.moveTo(X(h.l[0]), Y(h.l[1])); ctx.lineTo(X(h.l[2]), Y(h.l[3])); ctx.stroke(); if (h.ring) { ctx.lineWidth = 2 * dp; ctx.beginPath(); ctx.arc(X((h.l[0] + h.l[2]) / 2), Y((h.l[1] + h.l[3]) / 2), 12 * dp, 0, 7); ctx.stroke(); ctx.lineWidth = 4 * dp; } } }
  // drawing
  const d = S.draw; ctx.lineWidth = 2 * dp; ctx.strokeStyle = '#f5a623'; ctx.fillStyle = '#f5a62322';
  if (d.poly.length) { ctx.beginPath(); d.poly.forEach((p, i) => ctx[i ? 'lineTo' : 'moveTo'](X(p[0]), Y(p[1]))); if (d.mode !== 'poly') ctx.closePath(); ctx.fill(); ctx.stroke(); ctx.fillStyle = '#f5a623'; for (const p of d.poly) ctx.fillRect(X(p[0]) - 3 * dp, Y(p[1]) - 3 * dp, 6 * dp, 6 * dp); }
  if (d.box) { const b = d.box; ctx.strokeStyle = '#38bdf8'; ctx.fillStyle = '#38bdf822'; ctx.beginPath(); ctx.rect(X(b[0]), Y(b[1]), X(b[2]) - X(b[0]), Y(b[3]) - Y(b[1])); ctx.fill(); ctx.stroke(); }
  paintScan(dp); paintExtra(dp);
  const lg = []; if (S.A) lg.push(['#c9ced6', S.A.name + (S.ed ? ' (being edited)' : '')]); if (S.B) lg.push(['#3b82f6', S.B.name]); if (o.cloud && L.lasers) lg.push(['#ef4444', 'recorded lasers']); if (o.rows && L.track) lg.push(['#4ade80', 'wall match ≥55 %'], ['#facc15', '40–55 %'], ['#f87171', '<40 %']); if (o.track && L.track) lg.push(['#4ade80', 'confidence ≥0.7'], ['#facc15', '0.5–0.7'], ['#f87171', '<0.5'], ['#22d3ee', 'reflector mode']);
  $('#legend').replaceChildren(...lg.flatMap(([c, t]) => [el('i', { style: 'background:' + c }), t])); $('#legend').classList.toggle('hidden', !lg.length);
}
new ResizeObserver(draw).observe($('#canvasWrap'));
for (const c of $$('#toolbar input[data-l]')) { S.layers[c.dataset.l] = c.checked; c.onchange = () => { S.layers[c.dataset.l] = c.checked; draw(); }; }
const mpos = e => { const r = cv.getBoundingClientRect(), s = cv.width / r.width; return [(e.clientX - r.left) * s, (e.clientY - r.top) * s]; };
cv.addEventListener('wheel', e => { e.preventDefault(); const [px, py] = mpos(e), x = wx(px), y = wy(py), f = e.deltaY < 0 ? 1.2 : 1 / 1.2; S.view.k = Math.max(0.3, Math.min(400, S.view.k * f)); S.view.cx = x - (px - cv.width / 2) / S.view.k; S.view.cy = y + (py - cv.height / 2) / S.view.k; draw(); }, { passive: false });
let drag = null;
cv.onmousedown = e => { const [px, py] = mpos(e); if (S.draw.mode === 'box' && e.button === 0) { drag = { box: [wx(px), wy(py)] }; return; } if (S.draw.mode === '' && e.button === 0 && edDown(px, py)) { drag = { ed: true }; return; } drag = { px, py, cx: S.view.cx, cy: S.view.cy, moved: false }; };
window.addEventListener('mousemove', e => {
  const [px, py] = mpos(e);
  if (e.target === cv && !toastT) $('#coords').textContent = 'x ' + wx(px).toFixed(2) + '   y ' + wy(py).toFixed(2);
  if (!drag) return;
  if (drag.box) { S.draw.box = [drag.box[0], drag.box[1], wx(px), wy(py)]; draw(); return; }
  if (drag.ed) { edDrag(px, py); return; }
  if (Math.abs(px - drag.px) + Math.abs(py - drag.py) > 4) drag.moved = true;
  if (drag.moved) { S.view.cx = drag.cx - (px - drag.px) / S.view.k; S.view.cy = drag.cy + (py - drag.py) / S.view.k; draw(); }
});
window.addEventListener('mouseup', e => {
  if (!drag) return; const d = drag; drag = null;
  if (d.box) { const b = S.draw.box; if (b) { $('#box').value = [Math.min(b[0], b[2]), Math.min(b[1], b[3]), Math.max(b[0], b[2]), Math.max(b[1], b[3])].map(v => v.toFixed(2)).join(','); } setMode(''); return; }
  if (d.ed) { edUp(); return; }
  if (!d.moved && S.draw.mode === '' && e.target === cv && e.button === 0) { const [px, py] = mpos(e); edClick(px, py); return; }
  if (!d.moved && S.draw.mode === 'poly' && e.target === cv) { const [px, py] = mpos(e); S.draw.poly.push([+wx(px).toFixed(2), +wy(py).toFixed(2)]); syncRegion(); draw(); }
});
cv.ondblclick = () => { if (S.draw.mode === 'poly') { const p = S.draw.poly; if (p.length > 1 && Math.hypot(p[p.length - 1][0] - p[p.length - 2][0], p[p.length - 1][1] - p[p.length - 2][1]) < 0.05) p.pop(); setMode(''); syncRegion(); } };
const syncRegion = () => { $('#region').value = S.draw.poly.map(p => p.join(',')).join('  '); };
function setMode(m) { S.draw.mode = m; $('#polyBtn').classList.toggle('primary', m === 'poly'); $('#boxBtn').classList.toggle('primary', m === 'box'); draw(); }
$('#polyBtn').onclick = () => { if (S.draw.mode === 'poly') return setMode(''); S.draw.poly = []; syncRegion(); setMode('poly'); toast('click the corners of the region; double-click to finish'); };
$('#boxBtn').onclick = () => { S.draw.box = null; setMode(S.draw.mode === 'box' ? '' : 'box'); };
$('#clearDraw').onclick = () => { S.draw = { mode: '', poly: [], box: null }; $('#region').value = ''; $('#box').value = ''; S.ov.hi = null; setMode(''); };
$('#fitBtn').onclick = fit;
$('#region').onchange = () => { S.draw.poly = parsePts($('#region').value); draw(); };
$('#pngBtn').onclick = () => cv.toBlob(async b => { try { const r = await api('/api/save?name=' + encodeURIComponent('view ' + stamp() + '.png'), { method: 'POST', body: b }); toast('Saved ' + r.file); } catch (e) { toast(e.message); } });

// ---------------------------------------------------------------- tools
const parseRanges = t => (t.match(/\d+\s*-\s*\d+/g) || []).map(x => x.split('-').map(Number));
const parsePts = t => { const n = (t.match(/-?\d+(\.\d+)?/g) || []).map(Number), o = []; for (let i = 0; i + 1 < n.length; i += 2) o.push([n[i], n[i + 1]]); return o; };
const num = id => { const v = $(id).value.trim(); return v === '' ? undefined : +v; };
function argsFor(tool) {
  const drive = $('#selDrive').value, map = $('#selMap').value, map2 = $('#selMap2').value, from = num('#from'), to = num('#to'), name = $('#candName').value.trim();
  const need = (c, m) => { if (!c) throw new Error(m); };
  const needD = () => need(drive, 'Choose a recording first.'), needM = () => need(map, 'Choose a map first.'), need2 = () => need(map2, 'Choose a second map first.'), needN = () => need(name, 'Give the candidate a name.');
  switch (tool) {
    case 'drive_summary': needD(); return { drive, from, to };
    case 'replay': case 'rigid_fit': case 'find_pieces': needD(); needM(); return { drive, map, from, to };
    case 'drift': needD(); needM(); need(parseRanges($('#refR').value).length, 'Give the reference samples, for example 40-262.'); return { drive, map, from, to, ranges: parseRanges($('#refR').value) };
    case 'measure_turn': needD(); needM(); need(parseRanges($('#pieceR').value).length && parseRanges($('#refR').value).length, 'Give piece samples and reference samples, for example 40-262.');
      return { drive, pieceMap: map, refMap: map2 || undefined, pieceRanges: parseRanges($('#pieceR').value), refRanges: parseRanges($('#refR').value), stations: $('#turnSt').value.match(/[A-Za-z]+\d+/g) || [], from, to };
    case 'build_move_region': { needM(); needN(); const region = parsePts($('#region').value); need(region.length >= 3, 'Draw the region on the map first (Draw region).'); need(num('#tx') !== undefined && num('#ty') !== undefined && num('#th') !== undefined, 'Fill th, tx and ty (Measure turn fills them).'); return { base: map, name, th: num('#th'), tx: num('#tx'), ty: num('#ty'), region, anchor: $('#selAnchor').value || undefined }; }
    case 'build_merge_box': { needM(); need2(); needN(); const b = (($('#box').value.match(/-?\d+(\.\d+)?/g)) || []).map(Number); need(b.length === 4, 'Draw the box on the map first (Draw box).'); return { base: map, donor: map2, name, x0: b[0], y0: b[1], x1: b[2], y1: b[3] }; }
    case 'build_remove_lines': needM(); need2(); needN(); return { base: map, name, fromCheck: true, checkAgainst: map2 };
    case 'check_candidate': needM(); need2(); return { base: map2, candidate: map };
    case 'compare_maps': needM(); need2(); return { a: map2, b: map };
    case 'map_info': needM(); return { map };
    case 'detect_static': needM(); return { map };
    case 'detect_problems': needM(); needD(); return { map, drive, from, to };
  }
  return {};
}
let busy = false;
async function go(tool) {
  if (busy) return toast('Another analysis is still running.');
  let args; try { args = argsFor(tool); } catch (e) { return out(tool, e.message); }
  busy = true; for (const b of $$('button[data-run]')) b.disabled = true;
  try {
    const r = await run(tool === 'detect_static' ? 'detect_problems' : tool, args), d = r.data || {};
    out(tool.replace(/_/g, ' '), r.summary);
    if (tool === 'drive_summary') { S.ov = { track: d.track }; if (!S.A) toast('Pick a map to see the track on it.'); }
    if (tool === 'replay' || tool === 'rigid_fit') { if (!S.A || S.A.name !== args.map) await showMap(args.map); S.ov = { cloud: f32(d.cloud), rows: d.rows, jumps: (d.stretches || []).filter(s => s.Step >= 0.25) }; }
    if (tool.startsWith('detect_')) { if (!S.A || S.A.name !== args.map) await showMap(args.map); S.ov = { cloud: d.cloud ? f32(d.cloud) : null, rows: d.rows, jumps: (d.stretches || []).filter(s => s.Step >= 0.25), finds: d.findings || [], hi: (d.findings || []).filter(f => f.line).map(f => ({ l: f.line, c: f.level === 'fault' ? '#ff3b3b' : '#facc15', ring: true })) }; }
    if (tool === 'measure_turn') { $('#th').value = d.th.toFixed(6); $('#tx').value = d.tx.toFixed(4); $('#ty').value = d.ty.toFixed(4); }
    if (tool === 'check_candidate') S.ov.hi = (d.nearLines || []).map(n => ({ l: [n.a.X, n.a.Y, n.b.X, n.b.Y], c: '#ff3b3b', ring: true }));
    if (tool === 'compare_maps') S.ov.hi = [...(d.linesOnlyA || []).map(l => ({ l, c: '#60a5fa' })), ...(d.linesOnlyB || []).map(l => ({ l, c: '#f5a623' }))];
    if (tool.startsWith('build_')) { await refresh(); $('#selMap2').value = args.base; S.B = await loadView(args.base); await showMap(d.candidate); $('#candName').value = ''; }
    draw();
  } catch (e) { out(tool.replace(/_/g, ' ') + ' — did not run', e.message); }
  finally { busy = false; for (const b of $$('button[data-run]')) b.disabled = false; }
}
for (const b of $$('button[data-run]')) b.onclick = () => go(b.dataset.run);

// ---------------------------------------------------------------- dialogs
function dialog(title, bodyEl, okText, onOk) {
  $('#dlgTitle').textContent = title; $('#dlgBody').replaceChildren(bodyEl); $('#dlgOk').textContent = okText || 'Save'; $('#dlgOk').classList.toggle('hidden', !onOk);
  const d = $('#dlg'); d.onclose = async () => { if (d.returnValue === 'ok' && onOk) { try { await onOk(); } catch (e) { out(title, e.message); } } }; d.returnValue = ''; d.showModal();
}
const field = (label, props) => { const i = el(props && props.tag || 'input', props || {}); return [el('label', {}, label, i), i]; };
$('#wsBtn').onclick = () => { const [l, i] = field('Workspace folder (maps, candidates, drives and results are kept inside it)', { value: S.state.workspace }); dialog('Workspace', l, 'Use this folder', async () => { await post('/api/workspace', { path: i.value.trim() }); S.A = S.B = null; S.ov = {}; S.drive = ''; await refresh(); loadChat(true); draw(); }); };
$('#openBtn').onclick = () => post('/api/open', { what: '' });
$('#importBtn').onclick = () => $('#importFile').click();
$('#importFile').onchange = async e => { for (const f of e.target.files) { try { toast('importing ' + f.name + ' …'); await api('/api/upload?name=' + encodeURIComponent(f.name), { method: 'POST', body: f }); } catch (err) { out('Import', err.message); } } e.target.value = ''; toast(''); refresh(); };
const saveRobots = async list => { try { await post('/api/robots', list); } catch (e) { out('Robots', e.message); } refresh(); };
$('#addRobot').onclick = () => { const [l1, t] = field('One robot per line: name, then IP address', { tag: 'textarea', rows: 8, placeholder: 'AMR-01 10.0.0.26\nAMR-02 10.0.0.34' }); dialog('Add robots (read-only access)', el('div', {}, l1, el('p', { textContent: 'DRISHTI Map only reads position, lasers and map files from a robot. It cannot load a map or change anything on it.' })), 'Add', () => { const add = t.value.split('\n').map(x => x.trim().split(/[\s,;]+/)).filter(x => x.length >= 2 && x[0]).map(x => ({ name: x[0], ip: x[1] })); const have = new Set(add.map(x => x.name.toLowerCase())); return saveRobots([...S.ws.robots.filter(r => !have.has(r.name.toLowerCase())), ...add]); }); };
$('#fleetBtn').onclick = async () => { try { const r = await run('fleet_maps', { download: $('#fleetGet').checked }); out('maps on all robots', r.summary); if ((r.data.downloaded || []).length) refresh(); } catch (e) { out('maps on all robots', e.message); } };
async function robotStatus(name) { try { out('robot status', (await run('robot_status', { robot: name })).summary); } catch (e) { out('robot status', e.message); } }
async function robotMap(name) { try { const r = await run('robot_download_map', { robot: name }); out('robot map', r.summary); await refresh(); showMap(r.data.map); } catch (e) { out('robot map', e.message); } }
function recordDlg(name) { const [l1, n] = field('Recording name', { value: name.replace(/[^A-Za-z0-9_-]/g, '') + '_' + stamp() }), [l2, m] = field('Stop automatically after (minutes)', { type: 'number', value: 30, min: 1, max: 240 }); dialog('Record ' + name + ' (read-only)', el('div', {}, l1, l2, el('p', { textContent: 'Reads position, confidence and lasers about every 0.7 s. Start it, then drive the robot. Keep this window open.' })), 'Start recording', async () => { out('recording', (await run('record_start', { robot: name, name: n.value.trim(), minutes: +m.value })).summary); pollRec(); }); }
let recT = 0;
async function pollRec() {
  clearTimeout(recT);
  try { const r = await run('record_status', {}, true), d = r.data, b = $('#recBadge'); b.classList.toggle('hidden', !d.running); if (d.running) { b.textContent = '● REC ' + d.robot + ' · ' + d.stats.samples + ' samples · conf ' + (d.stats.confidence || 0).toFixed(2) + ' ' + (d.stats.station || '') + ' — click to stop'; recT = setTimeout(pollRec, 2000); } else if (d.drive && b.dataset.was === '1') { refresh(); } b.dataset.was = d.running ? '1' : ''; } catch (e) { }
}
$('#recBadge').onclick = async () => { const r = await run('record_stop', {}, true); out('recording', r.summary); pollRec(); refresh(); };

$('#settingsBtn').onclick = () => {
  const box = el('div'), rows = [];
  const add = p => {
    const [l1, name] = field('Name', { value: p.name || '' }), [l2, kind] = field('Type', { tag: 'select' }), [l3, url] = field('Address', { value: p.baseUrl || '' }), [l4, model] = field('Model', { value: p.model || '', placeholder: p.kind === 'anthropic' ? 'claude-opus-5-5' : 'model name on that server' }), [l5, key] = field('API key ' + (p.hasKey ? '(stored — leave empty to keep, “-” to remove)' : '(not needed for a local model)'), { type: 'password', autocomplete: 'off' });
    kind.append(el('option', { value: 'anthropic', textContent: 'Claude (Anthropic API)' }), el('option', { value: 'openai', textContent: 'OpenAI-compatible (OpenAI, Gemini, Mistral, LM Studio, Ollama …)' })); kind.value = p.kind || 'openai';
    const del = el('button', { type: 'button', className: 'mini', textContent: 'Remove' }), test = el('button', { type: 'button', textContent: 'Test connection' }), res = el('span', { className: 'testRes' });
    const d = el('div', { className: 'prov' }, el('div', { className: 'row' }, l1, l2), l3, el('div', { className: 'row' }, l4, l5), el('div', { className: 'row' }, test, res, el('span', { className: 'grow' }), del));
    const row = { p, name, kind, url, model, key, gone: false }; del.onclick = () => { row.gone = true; d.remove(); }; rows.push(row); box.append(d);
    test.onclick = async () => {
      test.disabled = true; res.className = 'testRes'; res.textContent = 'asking the model …';
      try { const r = await post('/api/provider/test', { provider: { id: p.id, name: name.value, kind: kind.value, baseUrl: url.value.trim(), model: model.value.trim() }, key: key.value }); res.className = 'testRes ok'; res.textContent = r.message; }
      catch (e) { res.className = 'testRes bad'; res.textContent = 'Not connected: ' + e.message; }
      test.disabled = false;
    };
  };
  S.state.providers.forEach(add);
  const PRESET = [['Claude (Anthropic)', 'anthropic', 'https://api.anthropic.com', 'claude-opus-5-5'], ['OpenAI', 'openai', 'https://api.openai.com/v1', ''], ['Google Gemini', 'openai', 'https://generativelanguage.googleapis.com/v1beta/openai', ''], ['Mistral', 'openai', 'https://api.mistral.ai/v1', ''], ['OpenRouter', 'openai', 'https://openrouter.ai/api/v1', ''], ['Ollama on this PC', 'openai', 'http://127.0.0.1:11434/v1', ''], ['LM Studio on this PC', 'openai', 'http://127.0.0.1:1234/v1', ''], ['Other OpenAI-compatible server', 'openai', '', '']];
  const preset = el('select', {}, ...PRESET.map((x, i) => el('option', { value: i, textContent: x[0] })));
  const addBtn = el('button', { type: 'button', textContent: '+ Add' }); addBtn.onclick = () => { const x = PRESET[+preset.value]; add({ id: 'model' + Date.now(), name: x[0], kind: x[1], baseUrl: x[2], model: x[3] }); box.append(more, about); };
  const more = el('div', { className: 'row addProv' }, 'Add a connection:', preset, addBtn);
  const about = el('div', {}, el('h3', { textContent: 'What this app can send to a robot (read-only list)' }), el('table', {}, ...Object.entries(S.state.robotRequests).flatMap(([port, m]) => Object.entries(m).map(([t, d]) => el('tr', {}, el('td', { textContent: 'port ' + port }), el('td', { textContent: 'request ' + t }), el('td', { textContent: d }))))), el('p', { textContent: 'DRISHTI Map ' + S.state.version + '. API keys are stored encrypted for this Windows user. The assistant is sent tool result text only.' }));
  box.append(more, about);
  dialog('Settings — AI models', box, 'Save', async () => {
    for (const r of rows) { if (r.gone) { await post('/api/provider/delete', { id: r.p.id }); continue; } await post('/api/provider', { provider: { id: r.p.id, name: r.name.value.trim() || r.p.id, kind: r.kind.value, baseUrl: r.url.value.trim(), model: r.model.value.trim() }, key: r.key.value, active: false }); }
    await refresh();
  });
};

// ---------------------------------------------------------------- assistant
let chatSeq = 0, chatT = 0;
$('#selProv').onchange = async () => { const p = S.state.providers.find(x => x.id === $('#selProv').value); if (p) { await post('/api/provider', { provider: p, key: '', active: true }); S.state.active = p.id; provNote(); } };
function addEvent(e) {
  const log = $('#chatLog');
  if (e.type === 'tool') { const d = el('details', { className: 'tool' }, el('summary', { textContent: '▸ ran ' + e.tool.replace(/_/g, ' ') }), el('pre', { textContent: e.text })); d.dataset.tool = e.tool; log.append(d); }
  else if (e.type === 'result') { const d = [...log.querySelectorAll('details.tool')].pop(); if (d) d.append(el('pre', { textContent: e.text })); if (e.tool && (e.tool.startsWith('build_') || e.tool.startsWith('robot_download') || e.tool === 'fleet_maps' || e.tool.startsWith('record_'))) refresh(); }
  else log.append(el('div', { className: 'msg ' + e.type, textContent: e.text }));
  log.scrollTop = log.scrollHeight;
}
async function loadChat(reset) {
  clearTimeout(chatT);
  if (reset) { chatSeq = 0; $('#chatLog').replaceChildren(); }
  try {
    const r = await api('/api/chat/events?after=' + chatSeq);
    for (const e of r.events) { addEvent(e); chatSeq = e.seq; }
    $('#chatSend').disabled = r.running; $('#chatStop').classList.toggle('hidden', !r.running); $('#chatLog').classList.toggle('working', r.running);
    if (r.running) chatT = setTimeout(loadChat, 600);
  } catch (e) { chatT = setTimeout(loadChat, 2000); }
}
async function send() { const t = $('#chatText').value.trim(); if (!t) return; try { await post('/api/chat/send', { text: t }); $('#chatText').value = ''; } catch (e) { addEvent({ type: 'error', text: e.message }); } loadChat(); }
$('#chatSend').onclick = send;
$('#chatText').onkeydown = e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } };
$('#chatStop').onclick = () => post('/api/chat/stop', {});
$('#chatNew').onclick = async () => { await post('/api/chat/reset', {}); loadChat(true); };
for (const b of $$('.tabs button')) b.onclick = () => { for (const x of $$('.tabs button')) x.classList.toggle('on', x === b); for (const t of $$('.tab')) t.classList.toggle('hidden', t.id !== 'tab-' + b.dataset.tab); if (b.dataset.tab === 'chat') loadChat(); if (b.dataset.tab === 'edit') edTab(); };

(async () => {
  if (new URLSearchParams(location.search).get('helpwin')) return;   // Help-only window (see edit.js)
  try {
    await refresh(); loadChat(true); pollRec();
    // optional start-up links (used by the tests): ?map=NAME&map2=NAME&drive=NAME&run=TOOL&tab=chat
    const q = new URLSearchParams(location.search);
    if (q.get('drive')) { S.drive = q.get('drive'); $('#selDrive').value = S.drive; }
    if (q.get('map')) await showMap(q.get('map'));
    if (q.get('map2')) await showMap(q.get('map2'), true);
    if (q.get('run')) await go(q.get('run'));
    if (q.get('view')) { const v = q.get('view').split(',').map(Number); S.view.cx = v[0]; S.view.cy = v[1]; S.view.k = v[2]; }
    if (q.get('tab')) $('.tabs button[data-tab=' + q.get('tab') + ']').click();
    if (q.get('ed')) { await edStart(); for (const o of JSON.parse(q.get('ed'))) await edOp(o); }
    if (q.get('pick')) { const v = q.get('pick').split(',').map(Number); edClick(X(v[0]), Y(v[1])); }
    if (q.get('help')) await openHelp(q.get('help'));
    if (q.get('scan')) await scanLoad(+q.get('scan'), true);
    if (!S.ws.maps.length && !S.ws.candidates.length && !S.ws.drives.length && !location.search.includes('help=')) openHelp('README');   // first start on an empty workspace
    refreshMarks(); document.title = 'DRISHTI Map' + (q.get('run') ? ' ready' : '');
  } catch (e) { out('Start', e.message); }
  draw();
})();
