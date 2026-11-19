'use strict';
// Hand editing of a map, detector markers, and the Help window. Uses the globals of app.js.

// ---------------------------------------------------------------- drawing on top of the map
const KIND = { line_on_path: 'line on path', walls_on_path: 'walls on path', low_match: 'low wall match', lonely_station: 'no path', structure: 'structure', not_rigid: 'not rigid' };
function paintExtra(dp) {
  const o = S.ov, k = S.view.k;
  for (const f of o.finds || []) {
    if (!f.hasXY || f.kind === 'jump') continue;                       // jumps are already circled
    const c = f.level === 'fault' ? '#ff3b3b' : f.level === 'warn' ? '#facc15' : '#7dd3fc', x = X(f.x), y = Y(f.y), r = (f.level === 'info' ? 5 : 9) * dp;
    ctx.strokeStyle = c; ctx.lineWidth = 2 * dp; ctx.beginPath(); ctx.moveTo(x, y - r); ctx.lineTo(x + r, y); ctx.lineTo(x, y + r); ctx.lineTo(x - r, y); ctx.closePath(); ctx.stroke();
    if (f.level !== 'info' || k > 12) { const t = (KIND[f.kind] || f.kind) + ' ' + (f.near || ''), tw = ctx.measureText(t).width; ctx.fillStyle = '#0e1014dd'; ctx.fillRect(x + r + 2 * dp, y - 9 * dp, tw + 8 * dp, 17 * dp); ctx.fillStyle = c; ctx.fillText(t, x + r + 6 * dp, y + 4 * dp); }
  }
  const e = S.ed; if (!e || !S.A) return;
  const v = S.A.v; ctx.strokeStyle = '#38bdf8'; ctx.lineWidth = 3 * dp;
  const ring = (x, y) => { ctx.beginPath(); ctx.arc(X(x), Y(y), 11 * dp, 0, 7); ctx.stroke(); };
  const s = e.sel;
  if (s && s.t === 'st') { const st = v.stations.find(q => q.n === s.n); if (st) ring(st.x, st.y); }
  if (s && s.t === 'path') { const i = v.pathNames.indexOf(s.n), p = v.paths[i]; if (p) { ctx.beginPath(); ctx.moveTo(X(p[0]), Y(p[1])); ctx.bezierCurveTo(X(p[2]), Y(p[3]), X(p[4]), Y(p[5]), X(p[6]), Y(p[7])); ctx.stroke(); const a = Math.atan2(Y(p[7]) - Y(p[5]), X(p[6]) - X(p[4])) || Math.atan2(Y(p[7]) - Y(p[1]), X(p[6]) - X(p[0])), ex = X(p[6]), ey = Y(p[7]), h = 14 * dp; ctx.beginPath(); ctx.moveTo(ex, ey); ctx.lineTo(ex - h * Math.cos(a - .4), ey - h * Math.sin(a - .4)); ctx.moveTo(ex, ey); ctx.lineTo(ex - h * Math.cos(a + .4), ey - h * Math.sin(a + .4)); ctx.stroke(); } }
  if (s && s.t === 'line') { const i = s.i * 4; if (i < v.L.length) { ctx.beginPath(); ctx.moveTo(X(v.L[i]), Y(v.L[i + 1])); ctx.lineTo(X(v.L[i + 2]), Y(v.L[i + 3])); ctx.stroke(); ring((v.L[i] + v.L[i + 2]) / 2, (v.L[i + 1] + v.L[i + 3]) / 2); } }
  if (e.from) { const st = v.stations.find(q => q.n === e.from); if (st) { ctx.strokeStyle = '#4ade80'; ring(st.x, st.y); } }
  if (e.drag) { ctx.strokeStyle = '#4ade80'; ring(e.drag.x, e.drag.y); ctx.fillStyle = '#4ade80'; ctx.fillText(e.drag.n + ' → ' + e.drag.x.toFixed(2) + ', ' + e.drag.y.toFixed(2), X(e.drag.x) + 14 * dp, Y(e.drag.y) - 8 * dp); }
}

// ---------------------------------------------------------------- picking
const dpr1 = () => Math.max(1, window.devicePixelRatio || 1);
function segDist(px, py, ax, ay, bx, by) { const vx = bx - ax, vy = by - ay, l = vx * vx + vy * vy; let t = l ? ((px - ax) * vx + (py - ay) * vy) / l : 0; t = Math.max(0, Math.min(1, t)); return Math.hypot(px - ax - t * vx, py - ay - t * vy); }
function pickStation(px, py) { let best = 14 * dpr1(), hit = null; for (const s of S.A.v.stations || []) { const d = Math.hypot(X(s.x) - px, Y(s.y) - py); if (d < best) { best = d; hit = s; } } return hit; }
function pickLine(px, py) { const L = S.A.v.L; let best = 9 * dpr1(), hit = -1; for (let i = 0; i < L.length; i += 4) { const d = segDist(px, py, X(L[i]), Y(L[i + 1]), X(L[i + 2]), Y(L[i + 3])); if (d < best) { best = d; hit = i / 4; } } return hit; }
function pickPath(px, py) {
  let best = 9 * dpr1(), hit = -1; const P = S.A.v.paths || [];
  for (let i = 0; i < P.length; i++) { const p = P[i]; let lx = X(p[0]), ly = Y(p[1]); for (let k = 1; k <= 16; k++) { const t = k / 16, u = 1 - t, x = X(u * u * u * p[0] + 3 * u * u * t * p[2] + 3 * u * t * t * p[4] + t * t * t * p[6]), y = Y(u * u * u * p[1] + 3 * u * u * t * p[3] + 3 * u * t * t * p[5] + t * t * t * p[7]), d = segDist(px, py, lx, ly, x, y); if (d < best) { best = d; hit = i; } lx = x; ly = y; } }
  return hit;
}

// ---------------------------------------------------------------- edit session
const HINT = {
  select: 'Click a station, a path or a feature line to see it and change it.',
  delline: 'Click a feature line to remove it. (The Feature lines layer is switched on.)',
  movest: 'Drag a station to its new place. Its paths and bin follow.',
  addst: 'Click where the new station goes.',
  addpath: 'Click the start station, then the end station.',
};
function edPanel() {
  const e = S.ed; $('#edBody').classList.toggle('hidden', !e); $('#edStart').classList.toggle('hidden', !!e); $('#edIntro').classList.toggle('hidden', !!e);
  if (!e) return;
  $('#edBase').textContent = 'Editing a copy of ' + e.base + '. The file on disk is not touched.';
  for (const b of $$('#edTools button')) b.classList.toggle('primary', b.dataset.ed === e.tool);
  $('#edHint').textContent = HINT[e.tool] + (e.tool === 'addpath' && e.from ? '  Start: ' + e.from + '.' : '');
  $('#edLog').replaceChildren(...e.log.map(t => el('li', { textContent: t }))); $('#edNone').classList.toggle('hidden', e.log.length > 0);
  $('#edUndo').disabled = !e.log.length; $('#edSave').disabled = !e.log.length;
  const keys = new Set(['direction', 'movestyle', 'maxspeed']); for (const t of S.A.v.pathInfo || []) for (const m of t.matchAll(/(\w+)=/g)) keys.add(m[1]);
  const ks = $('#edKey'), kv = ks.value; ks.replaceChildren(...[...keys].map(k => el('option', { value: k, textContent: k }))); if (keys.has(kv)) ks.value = kv;
  edSelPanel();
}
const val = t => { t = t.trim(); return t === 'true' ? true : t === 'false' ? false : t !== '' && !isNaN(+t) ? +t : t; };
function edSelPanel() {
  const e = S.ed, box = $('#edSel'), v = S.A.v, s = e.sel; box.replaceChildren();
  const btn = (t, fn, cls) => { const b = el('button', { type: 'button', textContent: t, className: cls || '' }); b.onclick = fn; return b; };
  if (!s) return;
  if (s.t === 'line') { const i = s.i * 4; if (i >= v.L.length) return; box.append(el('p', { className: 'selT', textContent: 'Feature line #' + s.i + '  (' + v.L[i].toFixed(2) + ', ' + v.L[i + 1].toFixed(2) + ') – (' + v.L[i + 2].toFixed(2) + ', ' + v.L[i + 3].toFixed(2) + ')' }), btn('Delete this line', () => { e.sel = null; edOp({ op: 'delete_lines', lines: [s.i] }); })); }
  if (s.t === 'path') {
    const i = v.pathNames.indexOf(s.n); if (i < 0) return;
    const key = el('select', {}, ...[...$('#edKey').options].map(o => el('option', { value: o.value, textContent: o.value }))), value = el('input', { placeholder: 'value' });
    box.append(el('p', { className: 'selT', textContent: 'Path ' + s.n }), el('p', { textContent: v.pathInfo[i] + '.  The arrow on the map shows the direction of travel.' }),
      el('div', { className: 'row' }, btn('Drive forward (direction 0)', () => edOp({ op: 'set_path', path: s.n, key: 'direction', value: 0 })), btn('Drive backward (direction 1)', () => edOp({ op: 'set_path', path: s.n, key: 'direction', value: 1 }))),
      el('div', { className: 'row' }, key, value, btn('Set', () => edOp({ op: 'set_path', path: s.n, key: key.value, value: val(value.value) }))),
      btn('Delete this path', () => { e.sel = null; edOp({ op: 'delete_path', path: s.n }); }));
  }
  if (s.t === 'st') {
    const st = v.stations.find(q => q.n === s.n); if (!st) return;
    const x = el('input', { type: 'number', step: 'any', value: st.x }), y = el('input', { type: 'number', step: 'any', value: st.y }), d = el('input', { type: 'number', step: 'any', placeholder: st.h ? (st.d * 180 / Math.PI).toFixed(1) : 'none' }), wp = el('input', { type: 'checkbox' });
    box.append(el('p', { className: 'selT', textContent: 'Station ' + st.n + '  (' + st.c + ')' }), el('p', { textContent: (st.p || 'no properties') + (st.i ? ', ignores direction' : '') }),
      el('label', { className: 'row' }, 'x', x, 'y', y, 'dir °', d),
      el('div', { className: 'row' }, btn('Apply position / direction', () => { const dd = d.value.trim() === '' ? undefined : +d.value; if (+x.value !== st.x || +y.value !== st.y) edOp({ op: 'move_station', station: st.n, x: +x.value, y: +y.value, dirDeg: dd }); else if (dd !== undefined) edOp({ op: 'set_station', station: st.n, dirDeg: dd }); else toast('nothing changed'); }),
        btn(st.i ? 'Use direction' : 'Ignore direction', () => edOp({ op: 'set_station', station: st.n, ignoreDir: !st.i }))),
      el('div', { className: 'row' }, btn('Delete this station', () => { e.sel = null; edOp({ op: 'delete_station', station: st.n, withPaths: wp.checked }); }), el('label', { className: 'row' }, wp, 'with its paths')));
  }
}
function edShow(r) {
  if (!r.edit) { S.ed = null; return edPanel(); }
  const keep = S.ed || { tool: 'select', sel: null, from: null, drag: null };
  S.ed = Object.assign(keep, { base: r.edit.base, log: r.edit.log || [] });
  S.A = { name: r.edit.base, v: prepView(r.view) }; $('#selMap').value = r.edit.base;
  edPanel(); refreshMarks(); draw();
}
async function edStart() {
  const base = $('#selMap').value; if (!base) return out('Edit', 'Choose a map first (Map, on the Tools tab or the list on the left).');
  try { toast('preparing a copy of ' + base + ' …'); S.ed = null; edShow(await post('/api/edit/start', { base })); S.ov = { finds: S.ov.finds, hi: S.ov.hi }; if (!$('#edName').value) $('#edName').value = base.replace(/_AMR-\d+_\d{8}-\d{4}$/, '') + '_edit'; toast(''); } catch (e) { out('Edit', e.message); }
}
async function edOp(op) {
  if (!S.ed) return;
  try { const r = await post('/api/edit/op', op); if (op.op === 'delete_lines') { S.ov.hi = null; S.ov.finds = (S.ov.finds || []).filter(f => f.kind !== 'line_on_path'); } edShow(r); out('Edits on ' + r.edit.base + ' (not saved yet)', r.edit.log.map((t, i) => (i + 1) + '. ' + t).join('\n')); toast(r.note.slice(0, 110)); }
  catch (e) { out('Edit — not applied', e.message); }
}
async function edEnd() { try { await post('/api/edit/cancel', {}); } catch (e) { } const base = S.ed && S.ed.base; S.ed = null; edPanel(); if (base) { try { S.A = await loadView(base); } catch (e) { S.A = null; } } draw(); }
async function edTab() { if (!S.ed) { try { const r = await api('/api/edit/state'); if (r.edit) edShow(r); } catch (e) { } } edPanel(); }
function edArea() { const p = S.draw.poly, b = S.draw.box; if (p.length >= 3 && S.draw.mode !== 'poly') return { region: p }; if (b) return { box: [Math.min(b[0], b[2]), Math.min(b[1], b[3]), Math.max(b[0], b[2]), Math.max(b[1], b[3])] }; out('Edit — not applied', 'Draw a Region or a Box on the map first (buttons above the map).'); return null; }
const areaOp = extra => { const a = edArea(); if (a) edOp(Object.assign(a, extra)); };

function edClick(px, py) {
  const e = S.ed; if (!e || !S.A) return;
  if (e.tool === 'select') { const st = pickStation(px, py), li = S.layers.lines ? pickLine(px, py) : -1, pa = pickPath(px, py); e.sel = st ? { t: 'st', n: st.n } : li >= 0 ? { t: 'line', i: li } : pa >= 0 ? { t: 'path', n: S.A.v.pathNames[pa] } : null; edPanel(); draw(); return; }
  if (e.tool === 'delline') { const li = pickLine(px, py); if (li < 0) return toast('no feature line there'); return edOp({ op: 'delete_lines', lines: [li] }); }
  if (e.tool === 'addpath') { const st = pickStation(px, py); if (!st) return toast('click a station'); if (!e.from) { e.from = st.n; edPanel(); draw(); return; } const from = e.from; e.from = null; if (from !== st.n) edOp({ op: 'add_path', from, to: st.n }); else { edPanel(); draw(); } return; }
  if (e.tool === 'addst') {
    const x = +wx(px).toFixed(3), y = +wy(py).toFixed(3), [l1, n] = field('Name (letters then number, e.g. LM700)', {}), [l2, like] = field('Copy type and properties from station (empty = the plainest one of the same kind)', {}), [l3, d] = field('Direction in degrees (empty = as the copied station)', { type: 'number', step: 'any' });
    dialog('New station at ' + x + ', ' + y, el('div', {}, l1, l2, l3, el('p', { textContent: 'A new station must also be added to the fleet scene before a task can use it.' })), 'Add', () => edOp({ op: 'add_station', name: n.value.trim(), like: like.value.trim() || undefined, x, y, dirDeg: d.value.trim() === '' ? undefined : +d.value }));
  }
}
function edDown(px, py) { const e = S.ed; if (!e || e.tool !== 'movest' || !S.A) return false; const st = pickStation(px, py); if (!st) return false; e.drag = { n: st.n, x: st.x, y: st.y }; return true; }
function edDrag(px, py) { const e = S.ed; if (e && e.drag) { e.drag.x = +wx(px).toFixed(3); e.drag.y = +wy(py).toFixed(3); draw(); } }
function edUp() { const e = S.ed; if (!e || !e.drag) return; const d = e.drag; e.drag = null; e.sel = { t: 'st', n: d.n }; edOp({ op: 'move_station', station: d.n, x: d.x, y: d.y }); }

$('#edStart').onclick = edStart;
$('#edCancel').onclick = () => { if (!S.ed.log.length || confirm('Discard ' + S.ed.log.length + ' edit(s)?')) edEnd(); };
$('#edUndo').onclick = async () => { try { edShow(await post('/api/edit/undo', {})); out('Edits on ' + S.ed.base + ' (not saved yet)', S.ed.log.map((t, i) => (i + 1) + '. ' + t).join('\n') || 'no edits'); } catch (e) { out('Edit', e.message); } };
for (const b of $$('#edTools button')) b.onclick = () => { S.ed.tool = b.dataset.ed; S.ed.from = null; if (b.dataset.ed === 'delline' && !S.layers.lines) { const c = $('#toolbar input[data-l=lines]'); c.checked = true; S.layers.lines = true; } setMode(''); edPanel(); draw(); };
$('#edErase').onclick = () => areaOp({ op: 'erase_walls' });
$('#edDelLines').onclick = () => areaOp({ op: 'delete_lines' });
$('#edTake').onclick = () => { const donor = $('#selMap2').value; if (!donor) return out('Edit — not applied', 'Choose the Second map (Tools tab): the walls are taken from it.'); areaOp({ op: 'take_walls', donor, mode: $('#edTakeMode').value, withLines: $('#edTakeLines').checked }); };
$('#edMoveW').onclick = () => { const n = id => +$(id).value || 0, px = $('#edPx').value.trim(), py = $('#edPy').value.trim(); areaOp({ op: 'move_walls', rotDeg: n('#edRot'), tx: n('#edTx'), ty: n('#edTy'), pivot: px !== '' && py !== '' ? [+px, +py] : undefined }); };
$('#edSetPaths').onclick = () => areaOp({ op: 'set_path', key: $('#edKey').value, value: val($('#edVal').value) });
$('#edSave').onclick = async () => {
  const name = $('#edName').value.trim(); if (!name) return out('Edit', 'Give the candidate a name.');
  try { const base = S.ed.base, r = await post('/api/edit/save', { name }); S.ed = null; edPanel(); out('candidate ' + r.data.candidate, r.summary); await refresh(); $('#selMap2').value = base; S.B = await loadView(base); await showMap(r.data.candidate); $('#edName').value = ''; draw(); }
  catch (e) { out('Edit — not saved', e.message); }
};

// ---------------------------------------------------------------- help
function md(t) {
  const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const inl = s => esc(s).replace(/\*\*(.+?)\*\*/g, '<b>$1</b>').replace(/`(.+?)`/g, '<code>$1</code>').replace(/\[\[([A-Z]+-?\d*)\]\]/g, '<a href="#" data-h="$1">$1</a>');
  const L = t.split('\n'), o = []; let i = 0;
  while (i < L.length) {
    const l = L[i];
    if (!l.trim()) { i++; continue; }
    let m;
    if ((m = l.match(/^(#{1,3}) (.*)/))) { o.push('<h' + m[1].length + '>' + inl(m[2]) + '</h' + m[1].length + '>'); i++; continue; }
    if (l.startsWith('|')) { const rows = []; while (i < L.length && L[i].startsWith('|')) { if (!/^\|[\s:|-]+\|?$/.test(L[i])) rows.push(L[i].replace(/^\||\|\s*$/g, '').split('|').map(c => inl(c.trim()))); i++; } o.push('<table>' + rows.map((r, k) => '<tr>' + r.map(c => (k ? '<td>' : '<th>') + c + (k ? '</td>' : '</th>')).join('') + '</tr>').join('') + '</table>'); continue; }
    if (/^(\d+\.|-) /.test(l)) { const ord = /^\d/.test(l), items = []; while (i < L.length && (/^(\d+\.|-) /.test(L[i]) || /^ {2,}\S/.test(L[i]))) { if (/^ {2,}/.test(L[i])) items[items.length - 1] += '<br>' + inl(L[i].trim()); else items.push(inl(L[i].replace(/^(\d+\.|-) /, ''))); i++; } o.push((ord ? '<ol>' : '<ul>') + items.map(x => '<li>' + x + '</li>').join('') + (ord ? '</ol>' : '</ul>')); continue; }
    if (l.startsWith('> ')) { const q = []; while (i < L.length && L[i].startsWith('> ')) { q.push(inl(L[i].slice(2))); i++; } o.push('<div class="callout">' + q.join('<br>') + '</div>'); continue; }
    const p = []; while (i < L.length && L[i].trim() && !/^(#{1,3} |\||> |(\d+\.|-) )/.test(L[i])) { p.push(inl(L[i])); i++; } o.push('<p>' + p.join(' ') + '</p>');
  }
  return o.join('');
}
let helpList = null;
async function openHelp(id) {
  try {
    if (!helpList) helpList = await api('/api/help');
    id = (typeof id === 'string' && id) || 'README';
    const a = await api('/api/help?id=' + encodeURIComponent(id)), nav = $('#helpNav'); nav.replaceChildren(); let g = '';
    for (const h of helpList) { if (h.group !== g) { g = h.group; nav.append(el('h3', { textContent: g })); } const b = el('button', { type: 'button', className: 'link' + (h.id === a.id ? ' on' : ''), textContent: (h.group === 'Start' ? '' : h.id + '  ') + h.title.replace(/^[A-Z]+-\d+\s*[·:.-]?\s*/, '') }); b.onclick = () => openHelp(h.id); nav.append(b); }
    const art = $('#helpArt'); art.innerHTML = md(a.text); art.scrollTop = 0;
    helpCur = a.id;
    if (!$('#helpDlg').open) $('#helpDlg').show();   // not modal: the user reads and works at the same time
  } catch (e) { out('Help', e.message); }
}
let helpCur = 'README';
const HELPWIN = new URLSearchParams(location.search).get('helpwin');   // this window shows Help only
// drag the Help panel by its title bar
$('#helpBar').addEventListener('pointerdown', e => {
  if (HELPWIN || e.target.closest('button')) return;
  const d = $('#helpDlg'), r = d.getBoundingClientRect(), dx = e.clientX - r.left, dy = e.clientY - r.top, bar = e.currentTarget;
  try { bar.setPointerCapture(e.pointerId); } catch (x) { }
  const mv = m => { d.style.left = Math.max(60 - r.width, Math.min(innerWidth - 60, m.clientX - dx)) + 'px'; d.style.top = Math.max(0, Math.min(innerHeight - 34, m.clientY - dy)) + 'px'; };
  const up = () => { bar.removeEventListener('pointermove', mv); bar.removeEventListener('pointerup', up); };
  bar.addEventListener('pointermove', mv); bar.addEventListener('pointerup', up);
});
$('#helpPop').onclick = () => { if (window.open('/?helpwin=' + encodeURIComponent(helpCur), 'drishtiHelp', 'popup,width=1000,height=860')) $('#helpDlg').close(); else out('Help', 'The separate window could not be opened. Help stays in this window; drag it by its title bar.'); };
if (HELPWIN) { document.body.classList.add('helpwin'); document.title = 'DRISHTI Map Help'; openHelp(HELPWIN); }
$('#helpArt').addEventListener('click', e => { const h = e.target.dataset && e.target.dataset.h; if (h) { e.preventDefault(); openHelp(h); } });
$('#helpBtn').onclick = () => openHelp('README');
$('#helpClose').onclick = () => HELPWIN ? window.close() : $('#helpDlg').close();
$('#connBtn').onclick = () => $('#settingsBtn').click();
