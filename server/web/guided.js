'use strict';
// Guided workflow: one case = one box on the map, taken through eight steps.
// 1 where/when  2 what happened  3 cause  4 evidence  5 fix options  6 build and prove  7 trial  8 roll out and watch
const GSTEPS = ['Where and when', 'What happened', 'Cause', 'Evidence', 'Fix options', 'Build and prove', 'Trial on one AMR', 'Roll out and watch'];
let G = null;
const esc = t => String(t == null ? '' : t).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
const gNew = () => ({ id: 'case-' + stamp(), name: '', step: 0, reached: 0, status: 'open', updated: '', hours: 24, robot: '', box: null, map: '' });
const cls = v => v < 0.3 ? 'bad' : v < 0.5 ? 'warn' : 'ok';
const li = a => a && a.length ? '<ul class="why">' + a.map(x => '<li>' + esc(x) + '</li>').join('') + '</ul>' : '';
const allMaps = () => [...S.ws.maps, ...S.ws.candidates].map(m => m.name);

async function gSave() {
  if (!G || !G.box) return;
  G.updated = new Date().toISOString(); G.name = G.name || ('Box ' + G.box.map(v => v.toFixed(0)).join(',') + (G.robot ? ' · ' + G.robot : ''));
  try { await post('/api/case', G); gCases(); } catch (e) { toast('Case not saved: ' + e.message); }
}
async function gCases() {
  let cs = []; try { cs = (await api('/api/cases')).cases || []; } catch (e) { }
  const ul = $('#caseList'); ul.replaceChildren(...cs.map(c => {
    const d = el('button', { className: 'mini', textContent: '×', title: 'Delete this case (maps and recordings stay)' });
    d.onclick = async e => { e.stopPropagation(); if (confirm('Delete the case "' + c.name + '"?')) { await post('/api/case/delete', { id: c.id }); if (G && G.id === c.id) { G = gNew(); gRender(); } gCases(); } };
    const l = el('li', { title: c.name }, el('span', { className: 'nm', textContent: c.name }), el('small', { textContent: c.status === 'closed' ? 'closed' : 'step ' + (c.step + 1) }), el('span', { className: 'acts' }, d));
    if (G && G.id === c.id) l.classList.add('on');
    l.onclick = async () => { try { G = await api('/api/case?id=' + encodeURIComponent(c.id)); if (G.map && allMaps().includes(G.map)) await showMap(G.map); gRender(); gCases(); } catch (e) { toast(e.message); } };
    return l;
  }), (() => { const n = el('li', { style: 'color:var(--dim)' }, '+ New case'); n.onclick = () => { G = gNew(); S.ov = {}; gRender(); gCases(); draw(); }; return n; })());
}

function gGo(i) { if (i < 0 || i > 7 || i > G.reached + 1) return; G.step = i; G.reached = Math.max(G.reached, i); gRender(); gSave(); }
const gNav = (next, ok = true) => `<div class="gnav">${G.step ? '<button data-act="back">← Back</button>' : ''}<span class="grow"></span>${next ? `<button class="primary" data-act="next"${ok ? '' : ' disabled'}>${next} →</button>` : ''}</div>`;

function gOverlay() {
  if (!G) return;
  const o = {};
  if (G.box) o.hbox = G.box;
  if (G.step <= 2 && G.points) o.track = G.points.map(p => ({ x: p.x, y: p.y, c: p.c }));
  if (G.step >= 6 && G.afterPoints) o.track = G.afterPoints.map(p => ({ x: p.x, y: p.y, c: p.c }));
  S.ov = o; S.gEv = (G.step === 3 || G.step === 4) ? G.evPts : null; draw();
}
function paintGuided(dp) {
  if (G && G.step === 5 && G.fix === 'piece' && G.pc && G.pc.pieces && S.gRows) { const c = G.pc, col = i => { for (const k of c.move || []) if (i >= c.pieces[k].range.From && i <= c.pieces[k].range.To) return '#f5a623'; for (const k of c.ref || []) if (i >= c.pieces[k].range.From && i <= c.pieces[k].range.To) return '#22d3ee'; return '#6b7280'; }; for (const r of S.gRows) { ctx.fillStyle = col(r.i); ctx.fillRect(X(r.x) - 2.5 * dp, Y(r.y) - 2.5 * dp, 5 * dp, 5 * dp); } if (c.seamBox && c.proof && c.proof.jumpsAfter > 0) { const b = c.seamBox; ctx.strokeStyle = '#38bdf8'; ctx.lineWidth = 2 * dp; ctx.strokeRect(X(b[0]), Y(b[3]), X(b[2]) - X(b[0]), Y(b[1]) - Y(b[3])); } if ((c.region || []).length >= 3) { ctx.strokeStyle = '#f5a623'; ctx.fillStyle = '#f5a62318'; ctx.lineWidth = 2 * dp; ctx.beginPath(); c.region.forEach((q, i) => ctx[i ? 'lineTo' : 'moveTo'](X(q[0]), Y(q[1]))); ctx.closePath(); ctx.fill(); ctx.stroke(); } }
  const e = S.gEv; if (!e) return; const sz = Math.max(2, Math.min(5, S.view.k / 10)) * dp;
  dots(e.seen, '#4ade80', sz); dots(e.unseen, '#f87171', sz); dots(e.always, '#60a5fa', sz); dots(e.sometimes, '#e879f9', sz);
}

function gRender() {
  if (!G) G = gNew();
  $('#gSteps').replaceChildren(...GSTEPS.map((s, k) => { const d = el('div', { className: 'st ' + (k === G.step ? 'on' : k <= G.reached ? 'done' : 'off') }, el('span', { className: 'n', textContent: k < G.step || (k <= G.reached && k !== G.step) ? '✓' : k + 1 }), s); if (k <= G.reached) d.onclick = () => gGo(k); return d; }));
  const p = $('#tab-guided'); p.innerHTML = [gS1, gS2, gS3, gS4, gS5, gS6, gS7, gS8][G.step](); p.scrollTop = 0;
  if (G.step === 0) { const m = $('#gMap'); m.replaceChildren(...allMaps().map(n => el('option', { value: n, textContent: n }))); m.value = G.map || (S.A && S.A.name) || m.value; G.map = m.value; m.onchange = () => { G.map = m.value; showMap(m.value); }; $('#gRobot').value = G.robot || ''; $('#gFrom').value = G.from || ''; $('#gTo').value = G.to || ''; }
  gOverlay();
  if (G.step === 3 && !G.src && !gBusy && G.box && G.map) gSources();
}
const R = () => G.report || {};

// ---- 1
function gS1() {
  const b = G.box;
  return `<h2>① Where and when</h2><p>Draw a box around the trouble area. Every robot that drove through it is looked at, so you see whether it is the place or one robot.</p>
  <div class="gcard"><b>Map</b><select id="gMap"></select></div>
  <div class="gcard"><b>Area</b><div class="row"><button data-act="draw">Draw box on the map</button><span class="dim">${b ? 'x ' + b[0].toFixed(1) + '…' + b[2].toFixed(1) + ', y ' + b[1].toFixed(1) + '…' + b[3].toFixed(1) : 'no box yet'}</span></div>
   <label>Robot the question is about (optional)<input id="gRobot" list="histRobots" placeholder="AMR-08"></label></div>
  <div class="gcard"><b>Time</b><div class="chips">${[6, 12, 24, 48, 72, 168].map(h => `<span class="chip${G.hours === h ? ' on' : ''}" data-hours="${h}">${h < 72 ? h + ' h' : h / 24 + ' days'}</span>`).join('')}<span class="chip${G.hours === 0 ? ' on' : ''}" data-hours="0">Custom</span></div>
   <div class="row${G.hours === 0 ? '' : ' hidden'}" id="gCustom">from <input id="gFrom" type="datetime-local"> to <input id="gTo" type="datetime-local"></div>
   <span class="dim">Back from now, on the fleet manager's clock. About a minute per day.</span></div>
  <p>Read-only: this reads the fleet manager's history. First time in this workspace: <button class="link" data-act="site">History sources …</button></p>
  <div class="gnav"><span class="grow"></span><button class="primary" data-act="find">Find →</button></div>`;
}
async function gFind() {
  G.robot = $('#gRobot').value.trim(); G.from = $('#gFrom').value; G.to = $('#gTo').value;
  if (!G.box) return out('guided', 'Draw a box on the map first.');
  const args = { box: G.box, robot: G.robot || undefined }; if (G.hours) args.hours = G.hours; else { if (!G.from || !G.to) return out('guided', 'Give the From and To times.'); args.from = G.from; args.to = G.to; }
  const r = await gRun('area_report', args); if (!r) return;
  G.report = r.data.report; G.points = r.data.points; G.range = r.data.from + ' to ' + r.data.to; G.text = r.summary;
  out('area report', r.summary); G.reached = 0;
  const b = G.box; S.view.cx = (b[0] + b[2]) / 2; S.view.cy = (b[1] + b[3]) / 2; S.view.k = Math.max(4, Math.min(60, Math.min(cv.width / (b[2] - b[0] + 24), cv.height / (b[3] - b[1] + 24))));
  gGo(1);
}
// ---- 2
function gS2() {
  const r = R(), n = Math.max(1, r.passes);
  return `<h2>② What happened</h2><p>${r.robots.length} robots, ${r.passes} passes through the box, ${esc(G.range)}.</p>
  <div class="gcard"><b>Passes</b><div class="gbar"><span style="width:${100 * r.fine / n}%;background:var(--ok)"></span><span style="width:${100 * r.low / n}%;background:var(--warn)"></span><span style="width:${100 * r.bad / n}%;background:var(--bad)"></span></div><span class="dim">${r.fine} fine · ${r.low} low (0.3 to 0.5) · ${r.bad} bad (below 0.3)</span></div>
  <div class="gcard"><b>Robots</b><table class="gt"><tr><th>robot</th><th>passes</th><th>lowest</th><th>bad</th><th>stops</th><th>errors</th></tr>${r.robots.map(w => `<tr${w.robot === G.robot ? ' class="hl"' : ''}><td>${esc(w.robot)}${w.lowAll ? ' <span class="bad" title="low everywhere in the plant">●</span>' : ''}</td><td>${w.passes}</td><td class="${cls(w.worst)}">${w.worst.toFixed(2)}</td><td>${w.bad || ''}</td><td>${w.stops || ''}</td><td class="bad" title="${esc((w.errors || []).join('\n'))}">${(w.errors || []).length || ''}</td></tr>`).join('')}</table></div>
  ${r.events && r.events.length ? '<div class="gcard"><b>Events</b>' + li(r.events) + '</div>' : ''}
  ${r.patterns && r.patterns.length ? '<div class="gcard"><b>Patterns</b>' + li(r.patterns) + '</div>' : ''}
  <p>The trips are drawn on the map, coloured by confidence. The full report is in the output panel below.</p>${gNav('Find the cause')}`;
}
// ---- 3
function gS3() {
  const r = R();
  return `<h2>③ Cause</h2>
  <div class="gcard verdict ${r.isMap ? 'v-map' : 'v-other'}"><b>${esc(r.verdict)}</b> <span class="rate ${r.rating === 'HIGH' ? 'high' : r.rating === 'LOW' ? 'lowr' : ''}">${esc(r.rating)}</span><p>${esc(r.summary)}</p></div>
  ${r.why && r.why.length ? '<div class="gcard"><b>Why</b>' + li(r.why) + '<span class="dim">The rating comes from the number of robots and passes, not from a guess.</span></div>' : ''}
  ${r.ruled && r.ruled.length ? '<div class="gcard"><b>Ruled out</b>' + li(r.ruled) + '</div>' : ''}
  ${r.unknown && r.unknown.length ? '<div class="gcard"><b>Not known yet</b>' + li(r.unknown) + '</div>' : ''}
  ${r.isMap ? gNav('Get evidence') : `<div class="gcard"><b>This does not look like a map problem.</b><p>A map fix would not help. Check what the verdict points at. You can close the case here, or carry on to the laser evidence anyway.</p></div><div class="gnav"><button data-act="back">← Back</button><span class="grow"></span><button data-act="next">Evidence anyway →</button><button class="primary" data-act="close">Close the case</button></div>`}`;
}
// ---- 4
function gS4() {
  const e = G.ev, src = G.src && G.src.drives ? G.src : null;
  const cover = src ? src.drives.filter(d => d.passes > 0) : [], rest = src ? src.drives.length - cover.length : 0;
  const diff = src ? src.versions.filter(v => !v.same && !v.sameAs) : [], same = src ? src.versions.filter(v => v.same).length : 0;
  const robots = S.ws.robots.map(r => `<option${r.name === (G.recRobot || G.robot) ? ' selected' : ''}>${esc(r.name)}</option>`).join('');
  return `<h2>④ Evidence</h2>
  <p>Steps 1 to 3 showed <b>that</b> robots have trouble here. This step finds <b>which walls</b> are wrong, so a fix can be built. For that it needs laser scans taken inside the box. They are not collected all the time: a recording has to be made.</p>
  ${gScanCard()}
  <div class="gcard"><b>A · Recordings that drive through this box</b>
   ${!src ? '<p class="dim">Looking at the recordings …</p>' : cover.length ? cover.map(d => `<label class="row"><input type="checkbox" class="gDrive" value="${esc(d.name)}"${(G.drives ? G.drives.includes(d.name) : true) ? ' checked' : ''}><span>${esc(d.name)}<br><small class="dim">${d.passes} pass${d.passes > 1 ? 'es' : ''}${d.dir ? ', heading ' + esc(d.dir) : ''}, lowest confidence ${d.min.toFixed(2)}, ${esc(d.when)}</small></span></label>`).join('') : '<p class="bad">No recording covers this box yet.</p>'}
   ${rest ? `<span class="dim">${rest} other recording${rest > 1 ? 's do' : ' does'} not drive through the box and cannot be used here.</span>` : ''}
   <p><b>Get a recording:</b> choose a robot whose route goes through the box${R().patterns && /heading (north|south|east|west)/.test(R().patterns.join(' ')) ? ', heading <b>' + R().patterns.join(' ').match(/heading (north|south|east|west)/)[1] + '</b> (that is the direction with the trouble)' : ''}.</p>
   <div class="row"><select id="gRecRobot">${robots}</select><button class="primary" data-act="arm" title="Waits until this robot comes near the box, records while it is there, and stops by itself">Record it when it drives through</button></div>
   <div class="row"><button data-act="rec">Record now …</button><button data-act="sources">Refresh the list</button></div>
   ${G.armed ? `<p class="warn">${esc(G.armed)}</p>` : ''}
   <span class="dim">This only reads from the robot. The program never sends a robot anywhere: it is sent by the fleet manager as usual.</span></div>
  <div class="gcard"><b>B · Compare what the robots see with the map</b>
   <p class="dim">Puts the recorded lasers on the map inside the box. It also tries earlier versions of the map that have different walls here: if one of them matches the lasers better, its walls are the fix.</p>
   ${diff.length ? diff.map(v => `<label class="row"><input type="checkbox" class="gVer" value="${esc(v.name)}"${(G.vers ? G.vers.includes(v.name) : v.pick) ? ' checked' : ''}><span>${esc(v.name)} <small class="dim">${esc(v.date)}</small></span></label>`).join('') : src ? '<p class="dim">No other version in this workspace has different walls in this box.</p>' : ''}
   ${same ? `<span class="dim">${same} other version${same > 1 ? 's have' : ' has'} the same walls here as ${esc(G.map)}: nothing to compare. So a map change did not alter the walls in this box.</span>` : ''}
   <div class="row"><button class="primary" data-act="evidence"${cover.length ? '' : ' disabled'}>Compare</button></div></div>
  ${e ? `<div class="gcard"><b>How well each version fits what the robots see here</b><table class="gt">${e.fits.map((f, i) => `<tr><td>${esc(f.map)}${i === 0 ? ' <span class="dim">(in question)</span>' : ''}</td><td class="${f.match >= 55 ? 'ok' : f.match >= 40 ? 'warn' : 'bad'}">${f.match.toFixed(0)} %</td></tr>`).join('')}</table><span class="dim">A good map gives 50 to 75 %.</span></div>
   <div class="gcard"><b>${esc(G.map)} in this box</b><div class="gkv"><span><i style="background:#4ade80"></i>walls seen by the lasers</span><span>${e.seenPct.toFixed(0)} %</span><span><i style="background:#f87171"></i>walls not seen (gone, or hidden)</span><span>${e.unseenPct.toFixed(0)} %</span>
    <span><i style="background:#60a5fa"></i>seen ${e.drives > 1 ? 'in every recording' : ''}, not in the map</span><span>${e.always} cells</span>${e.drives > 1 ? `<span><i style="background:#e879f9"></i>seen in some recordings only</span><span>${e.sometimes} cells</span>` : ''}</div>
    ${e.drives < 2 ? '<span class="dim">With one recording a parked bin and a new wall look the same. A second recording at another time tells them apart.</span>' : ''}</div>
   ${G.evFrom === 'scans' ? '<div class="gcard"><b>Position jumps: not checked</b><p>This came from single stored scans. A single scan cannot show a position jump or a misplaced piece of map: for that, record a drive in part A and press Compare in part B.</p></div>' : ''}
   <div class="gcard verdict ${(G.jumps || []).length ? 'v-map' : 'v-ok'}${G.evFrom === 'scans' ? ' hidden' : ''}"><b>Position jumps along the recording: ${(G.jumps || []).length}</b><p>${(G.jumps || []).length ? esc(G.jumps.map(j => j.step.toFixed(2) + ' m near ' + j.near).join('; ')) + '. A jump means a whole piece of the map sits in the wrong place. A box cannot fix that: the next step offers the piece move.' : 'The map does not make the robot jump on this recording. No piece of the map is misplaced along it.'}</p></div>` : ''}
  ${gNav('Fix options', !!e)}`;
}
function gScanCard() {
  const sc = G.scans, se = G.scanEv;
  return `<div class="gcard"><b>Recorded automatically by the apps server</b>
   <p class="dim">AMR Health stores laser scans from the robots named in its settings, at regular sample times and whenever a robot has a problem. Nobody has to record. DRISHTI Map reads them from the server (read-only).</p>
   <div class="row">last <select id="gScanH">${[24, 48, 72, 168].map(h => `<option value="${h}"${(G.scanH || 48) === h ? ' selected' : ''}>${h < 72 ? h + ' hours' : h / 24 + ' days'}</option>`).join('')}</select><button class="${sc ? '' : 'primary'}" data-act="scans">Look for stored scans</button><button data-act="hlogin" title="The AMR Health user name and password, kept encrypted for this Windows user">Sign in …</button></div>
   ${sc ? (sc.total ? '<table class="gt"><tr><th></th><th>robot</th><th>scans near the box</th><th>usable</th><th>at low confidence</th><th>newest</th></tr>' + sc.robots.map(r => `<tr><td><input type="checkbox" class="gScanRobot" value="${esc(r.robot)}" checked></td><td>${esc(r.robot)}</td><td>${r.scans}</td><td>${r.usable}</td><td>${r.low || ''}</td><td class="dim">${esc(r.newest)}</td></tr>`).join('') + '</table><div class="row"><button class="primary" data-act="scanev">Compare the stored scans with the map</button></div>' : '<p class="warn">No stored scan near this box in that time.' + (sc.stored ? ' AMR Health holds ' + sc.stored + ' scan' + (sc.stored > 1 ? 's' : '') + ', all taken elsewhere; the nearest was ' + Math.round(sc.nearest) + ' m from the box (' + esc(sc.where[0] ? sc.where.map(w => w.robot + ' at ' + w.place).slice(0, 3).join('; ') : '') + '). A scan helps here only if the robot was within about 12 m of the box.' : ' AMR Health holds no scan at all in that time.') + ' Scans exist only for the robots named in the AMR Health settings, and only where such a robot was at a sample time or had a problem.</p>') : ''}
   ${se ? '<table class="gt"><tr><th>robot</th><th>scans</th><th>laser points on a wall</th><th>moved from stored position</th></tr>' + se.robots.map(r => `<tr><td>${esc(r.robot)}${r.odd ? ' <span class="bad">check scanners</span>' : ''}</td><td>${r.scans}</td><td class="${r.odd ? 'bad' : r.match >= 55 ? 'ok' : 'warn'}">${r.match.toFixed(0)} %</td><td>${r.moved.toFixed(2)} m</td></tr>`).join('') + '</table><span class="dim">' + (se.robots.length < 3 ? 'Fewer than 3 robots: the scanners cannot be compared with each other yet.' : se.robots.some(r => r.odd) ? 'A robot clearly below the others sees the same place worse: look at its scanners (clean, level, height).' : 'The robots agree with each other: no scanner stands out.') + ' The walls are coloured on the map; the numbers are below.</span>' : ''}</div>`;
}
function gHealthLogin(then) {
  const [l1, u] = field('AMR Health user name', { autocomplete: 'off' }), [l2, pw] = field('Password', { type: 'password', autocomplete: 'off' });
  api('/api/health-login').then(r => { if (r.user) u.value = r.user; note.textContent = (r.server ? 'Server: ' + r.server + '. ' : 'No AMR Health server is set yet: enter it in History sources first. ') + 'The password is kept encrypted for this Windows user on this PC and is sent only to that server.'; }).catch(() => { });
  const note = el('p', {});
  dialog('Sign in to AMR Health (to read stored laser scans)', el('div', {}, l1, l2, note), 'Sign in', async () => { await post('/api/health-login', { user: u.value.trim(), pass: pw.value }); toast('Signed in to AMR Health'); if (then) then(); });
}
async function gScans() {
  G.scanH = +$('#gScanH').value; G.scanEv = null;
  const r = await gRun('stored_scans', { box: G.box, hours: G.scanH }, () => gScans()); if (!r) return;
  G.scans = r.data; out('stored scans', r.summary); gRender(); gSave();
}
async function gScanEvidence() {
  const robots = $$('.gScanRobot:checked').map(c => c.value); if (!robots.length) return out('guided', 'Tick at least one robot.');
  const r = await gRun('scan_evidence', { box: G.box, map: G.map, hours: G.scanH || 48, robots }, () => gScanEvidence()); if (!r) return;
  const d = r.data; G.evPts = { seen: f32(d.ptsSeen), unseen: f32(d.ptsUnseen), always: f32(d.ptsAlways), sometimes: f32(d.ptsSometimes) };
  G.ev = { fits: d.fits, bestOther: '', seenPct: d.seenPct, unseenPct: d.unseenPct, always: d.always, sometimes: d.sometimes, drives: d.scans }; G.evFrom = 'scans'; G.jumps = [];
  G.scanEv = { robots: d.robots, scans: d.scans }; out('stored scans against the map', r.summary); gRender(); gSave();
}
async function gSources() {
  if (!G.box || !G.map) return; G.src = { loading: true }; gRender();
  const r = await gRun('box_sources', { box: G.box, map: G.map }); G.src = r ? r.data : null; if (r) out('evidence sources', r.summary); gRender(); if (r) gSave();
  try { const hl = await api('/api/health-login'); if (hl.user && hl.server && !G.scans && G.step === 3) await gScans(); } catch (e) { }
}
async function gEvidence() {
  G.drives = $$('.gDrive:checked').map(c => c.value); G.vers = $$('.gVer:checked').map(c => c.value).slice(0, 5);
  if (!G.drives.length) return out('guided', 'Tick at least one recording that drives through the box, or record one.');
  const r = await gRun('box_evidence', { box: G.box, drives: G.drives, maps: [G.map, ...G.vers] }); if (!r) return;
  const d = r.data; G.evPts = { seen: f32(d.ptsSeen), unseen: f32(d.ptsUnseen), always: f32(d.ptsAlways), sometimes: f32(d.ptsSometimes) };
  G.ev = { fits: d.fits, bestOther: d.bestOther, seenPct: d.seenPct, unseenPct: d.unseenPct, always: d.always, sometimes: d.sometimes, drives: d.drives }; G.evText = r.summary; G.evFrom = 'drives';
  G.jumps = []; let txt = r.summary;
  for (const dn of G.drives.slice(0, 3)) { const p = await gRun('replay', { drive: dn, map: G.map }); if (!p) continue; for (const s of (p.data.stretches || [])) if (s.Step >= 0.5 && !s.Ambiguous && !s.TrackBreak) G.jumps.push({ drive: dn, step: s.Step, near: s.Near, x: s.X, y: s.Y }); txt += '\n\n' + p.summary; }
  out('laser evidence', txt); gRender(); gSave();
}
// ---- 5
function gS5() {
  const e = G.ev || { fits: [] }, cur = e.fits[0] || { match: 0 }, bo = e.bestOther, bf = e.fits.find(f => f.map === bo), jumps = (G.jumps || []).length;
  const pieceOn = jumps > 0 || !!(G.proof && G.proof.wallFail);
  const comes = e.drives > 1 && e.sometimes > 3 * e.always && e.sometimes > 30;
  const opt = (id, on, title, text) => `<div class="gopt${on ? (G.fix === id ? ' sel' : '') : ' no'}"${on ? ` data-fix="${id}"` : ''}><span class="r"></span><span><b>${title}</b><br><span class="dim">${text}</span></span></div>`;
  return `<h2>⑤ Fix options</h2><p>Only fixes that fit the evidence can be chosen.</p>
  ${opt('piece', pieceOn, 'Move a whole misplaced piece of the map', pieceOn ? (jumps ? `The recording shows ${jumps} position jump${jumps > 1 ? 's' : ''}. ` : 'The box fix put a wall on a path. ') + 'That is a piece of map attached in the wrong place. The program finds the piece, measures how far it is turned and shifted, and moves it with its stations, paths and lines together. This is the method that fixed the reference case.' : 'Not offered: the recording shows no position jump, so no piece is misplaced along it.')}
  ${opt('donor', !!bo, bo ? 'Take the walls in the box from ' + esc(bo) : 'Take the walls in the box from another version', bo ? `It fits the lasers ${bf.match.toFixed(0)} % here; the map in question ${cur.match.toFixed(0)} %. Stations and paths stay where they are.` + (jumps ? ' With a jump on the recording this is usually not enough: the checks in the next step will tell.' : '') : 'Not offered: no other version fits the lasers at least 5 points better here.')}
  ${opt('rescan', false, 'Rescan this area in RoboShop', cur.match < 55 && !bo && !pieceOn ? 'This is the way here: no version fits what the robots see (' + cur.match.toFixed(0) + ' %). The floor has changed since every scan.' : 'For when nothing above fits. Done in RoboShop, not here.')}
  ${comes ? '<div class="gcard"><b>Things come and go here</b><p>Most of what the lasers see outside the map is there in some recordings only. A storage position cannot be made right in a map: any version is wrong half the time.</p></div>' : ''}
  <div class="gcard"><label>Name of the new map file<input id="gName" value="${esc(G.candName || '')}" placeholder="PLANTMAP_v6"></label>${G.fix === 'donor' ? `<div class="row"><button data-act="draw">Redraw the box</button><span class="dim">x ${G.box[0].toFixed(1)}…${G.box[2].toFixed(1)}, y ${G.box[1].toFixed(1)}…${G.box[3].toFixed(1)}</span></div>` : ''}</div>
  ${gNav(G.fix === 'piece' ? 'Analyse the piece' : 'Build', G.fix === 'donor' || G.fix === 'piece')}`;
}
// ---- 6
function gS6() {
  if (G.fix === 'piece') return gS6Piece();
  const p = G.proof;
  if (!p) return `<h2>⑥ Build and prove</h2><p>Writes the new map as a file in <i>candidates</i>, runs every check, and lays the same recordings over it. The map on the robots is not touched.</p><div class="row"><button class="primary" data-act="build">Build ${esc(G.candName || '')}</button></div>${gNav('')}`;
  return `<h2>⑥ Build and prove</h2>
  <div class="gcard"><b>File</b><p>${esc(G.cand)}.smap in <i>candidates</i>. Not on any robot.</p></div>
  <div class="gcard verdict ${p.checksOk ? 'v-ok' : 'v-map'}"><b>Checks: ${p.checksOk ? 'all pass' : 'NOT all pass'}</b><p>${p.checksOk ? 'Same stations, paths, bins and areas; nothing closer to a wall than before; no feature line on a path.' : (p.wallFail ? 'With these walls a path or station would be too close to a wall. That is the sign of a misplaced piece of map: the walls belong somewhere else than the stations and paths that go with them, and a box cannot fix that. Go back to the fix options and choose "Move a whole misplaced piece". ' : '') + 'The full report is in the output panel. Do not go on with a failed check.'}</p></div>
  <div class="gcard"><b>The same lasers through both maps, in the box</b><div class="gkv3"><span></span><span class="h">before</span><span class="h">after</span><span>laser points on a wall</span><span class="${p.before >= 55 ? 'ok' : 'warn'}">${p.before.toFixed(0)} %</span><span class="${p.after >= 55 ? 'ok' : 'warn'}">${p.after.toFixed(0)} %</span></div>
   ${p.after < p.before + 3 ? '<p class="bad">The new file does not fit the lasers better. Go back to the fix options.</p>' : ''}</div>
  <p>A passed replay is evidence, not proof. It says nothing about a direction or a route the recordings did not drive.</p>${gNav('Go to trial', p.checksOk && p.after >= p.before + 3)}`;
}
async function gBuild() {
  const donor = G.ev.bestOther, b = G.box;
  const r = await gRun('build_merge_box', { base: G.map, donor, name: G.candName, x0: b[0], y0: b[1], x1: b[2], y1: b[3] }); if (!r) return;
  G.cand = r.data.candidate; G.candMd5 = (r.summary.match(/md5 ([0-9a-f]{32})/) || [])[1] || ''; out('build ' + G.cand, r.summary); await refresh();
  const checksOk = /RESULT: ALL CHECKS PASS/.test(r.summary);
  const e = await gRun('box_evidence', { box: b, drives: G.drives, maps: [G.cand, G.map] }); if (!e) return;
  out('build ' + G.cand, r.summary);
  const fit = n => (e.data.fits.find(f => f.map === n) || { match: 0 }).match;
  G.proof = { checksOk, wallFail: /FAIL +no (path|station) is closer to a wall/.test(r.summary), before: fit(G.map), after: fit(G.cand) }; gRender(); gSave(); await showMap(G.cand); gOverlay();
}
// ---- 6, the piece move: find the rigid pieces, measure the turn, move the whole piece, prove it
const rangeOf = p => [p.range.From, p.range.To];
function gS6Piece() {
  const c = G.pc || (G.pc = { drive: (G.drives || [])[0] || '' }), ps = c.pieces, t = c.turn, pr = c.proof;
  const ds = (G.src && G.src.drives ? G.src.drives : S.ws.drives).map(d => `<option${d.name === c.drive ? ' selected' : ''}>${esc(d.name)}</option>`).join('');
  const reg = c.region || [];
  return `<h2>⑥ Move a misplaced piece</h2><p>A jump shows at a seam; the cause is the whole piece behind it. Move the whole piece once. Moving part of it only moves the jump.</p>
  <div class="gcard"><b>1 · Recording that drives the whole piece</b><select id="gPcDrive">${ds}</select><span class="dim">Best is one drive from the part that is right, through the misplaced piece, and back to the part that is right.</span>
   <div class="row"><button class="${ps ? '' : 'primary'}" data-act="pieces">Find the rigid pieces</button></div></div>
  ${ps ? `<div class="gcard"><b>2 · Which piece is misplaced, which part is right</b><table class="gt"><tr><th>samples</th><th>from – to</th><th>length</th><th>step</th><th>turn</th><th title="the misplaced piece">move</th><th title="known to be right">right</th></tr>${ps.map((p, i) => `<tr><td>${p.range.From}–${p.range.To}</td><td>${esc(p.from)} – ${esc(p.to)}</td><td>${p.length.toFixed(0)} m</td><td class="${p.stepAtSeam >= 0.5 ? 'bad' : ''}">${i ? p.stepAtSeam.toFixed(2) + ' m' : ''}</td><td>${i ? p.turnDeg.toFixed(1) + '°' : ''}</td><td><input type="checkbox" class="gPcMove" value="${i}"${(c.move || []).includes(i) ? ' checked' : ''}></td><td><input type="checkbox" class="gPcRef" value="${i}"${(c.ref || []).includes(i) ? ' checked' : ''}></td></tr>`).join('')}</table>
    <span class="dim">Each row is a stretch over which the map is rigid against the lasers. "step" and "turn" say how it sits against the row above. On the map: <span style="color:#f5a623">orange</span> = to move, <span style="color:#22d3ee">cyan</span> = right. <b>The program cannot know which side is right.</b> Its first guess is that the drive started on the right part. The right part is the one that looks the same in older versions of the map; the misplaced piece is the one that was scanned or attached later. Change the ticks if needed.</span>
    <div class="row"><button class="${t ? '' : 'primary'}" data-act="measure">Measure the turn</button></div></div>` : ''}
  ${t ? `<div class="gcard"><b>3 · The correction</b><div class="gkv"><span>turn</span><span>${t.rotDeg.toFixed(2)}° ± ${t.spreadDeg.toFixed(2)}°</span><span>shift</span><span>${t.tx.toFixed(2)} m, ${t.ty.toFixed(2)} m</span></div><span class="dim">Measured three times; the ± is the spread. At 40 m from the turning point, 0.2° is about 0.14 m.</span></div>
   <div class="gcard"><b>4 · Mark the whole piece on the map</b><p class="dim">Click corners around everything that belongs to the misplaced piece (the orange track and the walls, stations and paths beside it); double-click to finish. Cut the route where the orange track begins.</p>
    <div class="row"><button class="${reg.length >= 3 ? '' : 'primary'}" data-act="region">Draw the region</button><span class="${reg.length >= 3 ? 'ok' : 'dim'}">${reg.length >= 3 ? reg.length + ' corners' : 'not drawn yet'}</span></div>
    <label>Walls that must stay where they are (optional: an older map that was right outside the piece)<select id="gPcAnchor"><option value="">—</option>${allMaps().filter(n => n !== G.map).map(n => `<option${n === c.anchor ? ' selected' : ''}>${esc(n)}</option>`).join('')}</select></label>
    <div class="row"><button class="primary" data-act="pbuild"${reg.length >= 3 ? '' : ' disabled'}>Build ${esc(G.candName || '')} and prove it</button></div></div>` : ''}
  ${pr ? `<div class="gcard verdict ${pr.checksOk ? 'v-ok' : 'v-map'}"><b>Checks: ${pr.checksOk ? 'all pass' : 'NOT all pass'}</b><p>${pr.checksOk ? 'Stations and paths inside the piece moved together and kept their spacing; nothing is closer to a wall than before.' : 'Read the report in the output panel. Usually the region cut through something that belongs together: redraw it and build under a new name.'}</p></div>
   <div class="gcard"><b>The same recording through both maps</b><div class="gkv3"><span></span><span class="h">before</span><span class="h">after</span><span>position jumps</span><span class="${pr.jumpsBefore ? 'bad' : 'ok'}">${pr.jumpsBefore}</span><span class="${pr.jumpsAfter ? 'bad' : 'ok'}">${pr.jumpsAfter}</span><span>laser points on a wall</span><span>${pr.fitBefore.toFixed(0)} %</span><span>${pr.fitAfter.toFixed(0)} %</span></div>
    ${pr.jumpsAfter ? '<p class="bad">A jump is still there. The region or the piece choice is not right yet: a partial move only moves the jump.</p>' : ''}</div>` : ''}
  ${pr && pr.checksOk && pr.jumpsAfter > 0 ? gSeamCard(c) : ''}
  ${gNav('Go to trial', !!pr && pr.checksOk && (pr.jumpsAfter === 0 || (!!c.seam && c.seam.checksOk && c.seam.jumpsAfter === 0)))}`;
}
function gSeamCard(c) {
  const b = c.seamBox, f = c.seamFits, sm = c.seam;
  return `<div class="gcard"><b>5 · Repair what is left at the seam</b><p class="dim">The piece is in place, but ${esc((c.afterJumps || []).map(j => j.step.toFixed(2) + ' m near ' + j.near).join(' and '))} remain. Where the piece meets the rest, the old walls of both placements overlap or were cleaned away. Take the walls for just that stretch from the map version that fits the lasers best there.</p>
   <div class="row"><button data-act="seambox">Redraw the stretch</button><span class="dim">${b ? 'x ' + b[0].toFixed(1) + '…' + b[2].toFixed(1) + ', y ' + b[1].toFixed(1) + '…' + b[3].toFixed(1) + ' (blue box on the map)' : 'no box'}</span></div>
   <div class="row"><button class="${f ? '' : 'primary'}" data-act="seamfind"${b ? '' : ' disabled'}>Find the best walls for this stretch</button></div>
   ${f ? '<table class="gt">' + f.map((x, i) => `<tr><td>${esc(x.map)}${i === 0 ? ' <span class="dim">(the moved map)</span>' : ''}</td><td class="${x.match >= 55 ? 'ok' : x.match >= 40 ? 'warn' : 'bad'}">${x.match.toFixed(0)} %</td></tr>`).join('') + '</table>' + (c.seamDonor ? `<div class="row"><button class="primary" data-act="seambuild">Take the walls from ${esc(c.seamDonor)} and prove it</button></div>` : '<p class="bad">No version fits this stretch better than the moved map. It needs a rescan in RoboShop.</p>') : ''}
   ${sm ? `<div class="gkv3"><span></span><span class="h">moved</span><span class="h">repaired</span><span>position jumps</span><span class="bad">${c.proof.jumpsAfter}</span><span class="${sm.jumpsAfter ? 'bad' : 'ok'}">${sm.jumpsAfter}</span><span>laser points on a wall</span><span>${c.proof.fitAfter.toFixed(0)} %</span><span>${sm.fitAfter.toFixed(0)} %</span><span>checks</span><span></span><span class="${sm.checksOk ? 'ok' : 'bad'}">${sm.checksOk ? 'all pass' : 'NOT all pass'}</span></div><span class="dim">File: ${esc(G.cand)}.smap</span>` : ''}</div>`;
}
async function gSeamFind() {
  const c = G.pc; c.seam = null;
  const src = await gRun('box_sources', { box: c.seamBox, map: c.moved }); if (!src) return;
  const vers = src.data.versions.filter(v => !v.same && !v.sameAs && v.walls > 0 && v.name !== G.cand).slice(0, 4).map(v => v.name);
  if (!vers.length) { c.seamFits = [{ map: c.moved, match: 0 }]; c.seamDonor = ''; return gRender(); }
  const e = await gRun('box_evidence', { box: c.seamBox, drives: [c.drive], maps: [c.moved, ...vers] }); if (!e) return;
  c.seamFits = e.data.fits; c.seamDonor = e.data.bestOther || ''; out('walls for the seam', e.summary); gRender(); gSave();
}
async function gSeamBuild() {
  const c = G.pc, b = c.seamBox, name = G.candName + '_b';
  const r = await gRun('build_merge_box', { base: c.moved, donor: c.seamDonor, name, x0: b[0], y0: b[1], x1: b[2], y1: b[3] }); if (!r) return;
  G.cand = r.data.candidate; G.candMd5 = (r.summary.match(/md5 ([0-9a-f]{32})/) || [])[1] || ''; await refresh();
  const rp = await gRun('replay', { drive: c.drive, map: G.cand }); if (!rp) return;
  c.seam = { checksOk: /RESULT: ALL CHECKS PASS/.test(r.summary), jumpsAfter: bigJumps(rp.data), fitAfter: (rp.summary.match(/average wall match (\d+)/) || [0, 0])[1] * 1 };
  out('build ' + G.cand, r.summary + '\n\n' + rp.summary); gRender(); gSave(); await showMap(G.cand); gOverlay();
}
const bigJumps = d => (d.stretches || []).filter(s => s.Step >= 0.5 && !s.Ambiguous && !s.TrackBreak).length;
async function gPieces() {
  const c = G.pc; c.drive = $('#gPcDrive').value; c.turn = null; c.proof = null;
  const rp = await gRun('replay', { drive: c.drive, map: G.map }); if (!rp) return;
  S.gRows = rp.data.rows.map(r => ({ i: r.i, x: r.x, y: r.y })); c.jumpsBefore = bigJumps(rp.data); c.fitBefore = (rp.summary.match(/average wall match (\d+)/) || [0, 0])[1] * 1;
  const r = await gRun('find_pieces', { drive: c.drive, map: G.map }); if (!r) return;
  c.pieces = r.data || []; out('rigid pieces', r.summary);
  // first guess: the piece behind the largest seam is the misplaced one; everything before that seam is right
  let k = 0; c.pieces.forEach((p, i) => { if (i && p.stepAtSeam > (c.pieces[k].stepAtSeam || 0)) k = i; });
  c.move = k ? [k] : []; c.ref = k ? c.pieces.map((p, i) => i).filter(i => i < k) : [];
  gRender(); gSave();
}
async function gMeasure() {
  const c = G.pc; c.move = $$('.gPcMove:checked').map(x => +x.value); c.ref = $$('.gPcRef:checked').map(x => +x.value);
  if (!c.move.length || !c.ref.length) return out('guided', 'Tick at least one piece to move and one piece that is right.');
  if (c.move.some(i => c.ref.includes(i))) return out('guided', 'A piece cannot be both moved and right.');
  const r = await gRun('measure_turn', { drive: c.drive, pieceMap: G.map, pieceRanges: c.move.map(i => rangeOf(c.pieces[i])), refRanges: c.ref.map(i => rangeOf(c.pieces[i])), stations: [] }); if (!r) return;
  c.turn = { th: r.data.th, tx: r.data.tx, ty: r.data.ty, rotDeg: r.data.rotDeg, spreadDeg: r.data.spreadDeg }; out('measure turn', r.summary); gRender(); gSave();
}
async function gPieceBuild() {
  const c = G.pc; c.anchor = $('#gPcAnchor').value;
  const r = await gRun('build_move_region', { base: G.map, name: G.candName, th: c.turn.th, tx: c.turn.tx, ty: c.turn.ty, region: c.region, anchor: c.anchor || undefined }); if (!r) return;
  G.cand = r.data.candidate; G.candMd5 = (r.summary.match(/md5 ([0-9a-f]{32})/) || [])[1] || ''; out('build ' + G.cand, r.summary); await refresh();
  const rp = await gRun('replay', { drive: c.drive, map: G.cand }); if (!rp) return;
  c.moved = G.cand; c.seam = null; c.seamFits = null; c.seamDonor = '';
  c.afterJumps = (rp.data.stretches || []).filter(x => x.Step >= 0.5 && !x.Ambiguous && !x.TrackBreak).map(x => ({ step: x.Step, near: x.Near, x: x.X, y: x.Y }));
  c.seamBox = c.afterJumps.length ? [Math.min(...c.afterJumps.map(j => j.x)) - 7, Math.min(...c.afterJumps.map(j => j.y)) - 6, Math.max(...c.afterJumps.map(j => j.x)) + 7, Math.max(...c.afterJumps.map(j => j.y)) + 6].map(v => +v.toFixed(1)) : null;
  c.proof = { checksOk: /RESULT: ALL CHECKS PASS/.test(r.summary), jumpsBefore: c.jumpsBefore, jumpsAfter: bigJumps(rp.data), fitBefore: c.fitBefore, fitAfter: (rp.summary.match(/average wall match (\d+)/) || [0, 0])[1] * 1 };
  out('build ' + G.cand, r.summary + '\n\n' + rp.summary); gRender(); gSave(); await showMap(G.cand); gOverlay();
}
// ---- 7
function gS7() {
  const t = G.trial, ok = G.trialMapOk;
  return `<h2>⑦ Trial on one AMR</h2><p><b>You</b> load <b>${esc(G.cand)}</b> on one AMR in RoboShop and drive through the box. This program only reads.</p>
  <div class="gcard"><b>1 · Trial robot</b><div class="row"><select id="gTrialRobot">${S.ws.robots.map(r => `<option${r.name === G.trialRobot ? ' selected' : ''}>${esc(r.name)}</option>`).join('')}</select><button data-act="trialmap">Is the new map on it?</button></div>${ok === true ? '<span class="ok">✔ The robot runs this exact file.</span>' : ok === false ? '<span class="bad">✘ The robot runs a different file. Load the new map first.</span>' : ''}</div>
  <div class="gcard"><b>2 · Record the trial drive</b><div class="row"><button data-act="trialrec">Record …</button><span class="dim">Start before the box, stop after it. Both directions if robots use both.</span></div></div>
  <div class="gcard"><b>3 · Judge it</b><div class="row"><select id="gTrialDrive">${S.ws.drives.map(d => `<option${d.name === G.trialDrive ? ' selected' : ''}>${esc(d.name)}</option>`).join('')}</select><button class="primary" data-act="trialjudge">Judge the trial</button></div></div>
  ${t ? `<div class="gcard verdict ${t.pass ? 'v-ok' : 'v-map'}"><b class="${t.pass ? 'ok' : 'bad'}">${t.pass ? 'PASS' : 'FAIL'}</b><div class="gkv3"><span></span><span class="h">before</span><span class="h">trial</span><span>lowest confidence in the box</span><span>${t.before.toFixed(2)}</span><span class="${cls(t.min)}">${t.min.toFixed(2)}</span><span>position jumps</span><span></span><span class="${t.jumps ? 'bad' : 'ok'}">${t.jumps}</span></div></div>` : ''}
  <div class="gnav"><button data-act="back">← Back</button>${t && !t.pass ? '<button data-act="tofix">Failed: other option</button>' : ''}<span class="grow"></span><button class="primary" data-act="next"${t && t.pass ? '' : ' disabled'}>Roll out →</button></div>`;
}
// ---- 8
function gS8() {
  const f = G.fleet, a = G.after, r = R();
  return `<h2>⑧ Roll out and watch</h2><p><b>You</b> load <b>${esc(G.cand)}</b> on all AMRs. This program confirms it and then watches the same box.</p>
  <div class="gcard"><b>1 · On the robots</b><div class="row"><button data-act="fleet">Check every robot's map</button></div>${f ? `<span class="${f.same === f.up && f.up ? 'ok' : 'warn'}">${f.same} of ${f.total} robots run this exact file${f.up < f.total ? '; ' + (f.total - f.up) + ' not reachable' : ''}.</span>` : ''}</div>
  <div class="gcard"><b>2 · The same box, after the change</b><div class="row">last <select id="gAfterH"><option>6</option><option>12</option><option selected>24</option><option>48</option></select> hours <button data-act="after">Compare with before</button></div>
   ${a ? `<div class="gkv3"><span></span><span class="h">before</span><span class="h">after</span><span>passes</span><span>${r.passes}</span><span>${a.passes}</span><span>typical lowest confidence</span><span class="${cls(r.typical)}">${r.typical.toFixed(2)}</span><span class="${cls(a.typical)}">${a.typical.toFixed(2)}</span><span>passes below 0.3</span><span>${r.bad}</span><span class="${a.bad ? 'bad' : 'ok'}">${a.bad}</span><span>verdict</span><span>${esc(r.verdict)}</span><span>${esc(a.verdict)}</span></div>` : '<span class="dim">Run this a day or two after the roll-out.</span>'}</div>
  <div class="gnav"><button data-act="back">← Back</button><span class="grow"></span><button class="primary" data-act="close">Close the case</button></div>`;
}

let gBusy = false;
async function gRun(tool, args, retry) {
  if (gBusy || busy) { toast('Another analysis is still running.'); return null; }
  gBusy = true; busy = true; for (const b of $$('#tab-guided button')) b.disabled = true;
  try { return await run(tool, args); } catch (e) { if (/sign in to AMR Health/i.test(e.message)) { out('AMR Health', 'Sign in to AMR Health to read the stored laser scans.'); setTimeout(() => gHealthLogin(retry), 50); } else out(tool.replace(/_/g, ' ') + ' — did not run', e.message); return null; }
  finally { gBusy = false; busy = false; for (const b of $$('#tab-guided button')) b.disabled = false; }
}
$('#tab-guided').addEventListener('click', async ev => {
  const t = ev.target.closest('[data-act],[data-hours],[data-fix]'); if (!t || t.disabled) return;
  if (t.dataset.hours !== undefined) { G.hours = +t.dataset.hours; G.robot = $('#gRobot').value.trim(); G.from = $('#gFrom').value; G.to = $('#gTo').value; return gRender(); }
  if (t.dataset.fix) { G.fix = t.dataset.fix; G.candName = $('#gName').value.trim(); G.proof = null; return gRender(); }
  switch (t.dataset.act) {
    case 'back': return gGo(G.step - 1);
    case 'next': if (G.step === 4) { G.candName = $('#gName').value.trim(); if (!G.candName) return out('guided', 'Give the new map file a name.'); G.proof = null; if (G.pc) G.pc.proof = null; } return gGo(G.step + 1);
    case 'tofix': return gGo(4);
    case 'draw': if (!S.A) return out('guided', 'Choose a map first.'); S.draw.box = null; setMode('box'); toast('drag a box on the map'); gWaitBox(); return;
    case 'site': return $('#histSite').click();
    case 'find': return gFind();
    case 'rec': G.recRobot = $('#gRecRobot').value; return recordDlg(G.recRobot);
    case 'sources': await refresh(); return gSources();
    case 'arm': { G.recRobot = $('#gRecRobot').value; const nm = G.recRobot.replace(/[^A-Za-z0-9_-]/g, '') + '_box_' + stamp(); const r = await gRun('record_arm', { robot: G.recRobot, name: nm, box: G.box }); if (!r) return; out('armed recording', r.summary); G.armed = 'Armed for ' + G.recRobot + ': it is recorded when it drives through the box. Keep this window open.'; gRender(); pollRec(); gArmWatch(); return; }
    case 'scans': return gScans();
    case 'scanev': return gScanEvidence();
    case 'hlogin': return gHealthLogin();
    case 'pieces': return gPieces();
    case 'measure': return gMeasure();
    case 'region': if (!S.A) return; G.pc.anchor = $('#gPcAnchor').value; $('#polyBtn').click(); gWaitPoly(); return;
    case 'pbuild': return gPieceBuild();
    case 'seamfind': return gSeamFind();
    case 'seambuild': return gSeamBuild();
    case 'seambox': if (!S.A) return; S.draw.box = null; setMode('box'); toast('drag a box around the stretch'); { const t = setInterval(() => { if (S.draw.mode !== 'box') { clearInterval(t); const b = S.draw.box; if (b) { G.pc.seamBox = [Math.min(b[0], b[2]), Math.min(b[1], b[3]), Math.max(b[0], b[2]), Math.max(b[1], b[3])].map(v => +v.toFixed(2)); G.pc.seamFits = null; G.pc.seam = null; S.draw.box = null; gRender(); } } }, 200); } return;
    case 'evidence': return gEvidence();
    case 'build': return gBuild();
    case 'trialmap': { G.trialRobot = $('#gTrialRobot').value; const r = await gRun('robot_status', { robot: G.trialRobot }); if (!r) return; out('robot status', r.summary); G.trialMapOk = !!G.candMd5 && r.summary.includes(G.candMd5); gRender(); return gSave(); }
    case 'trialrec': return recordDlg($('#gTrialRobot').value);
    case 'trialjudge': { G.trialDrive = $('#gTrialDrive').value; const r = await gRun('trial_result', { drive: G.trialDrive, box: G.box, before: R().typical || 0 }); if (!r) return; out('trial result', r.summary); G.trial = r.data; gRender(); return gSave(); }
    case 'fleet': { const r = await gRun('fleet_maps', {}); if (!r) return; out('maps on all robots', r.summary); const rows = r.data.rows || [], up = rows.filter(x => x.ok).length; G.fleet = { total: rows.length, up, same: G.candMd5 ? (r.summary.match(new RegExp(G.candMd5.slice(0, 8), 'g')) || []).length : 0 }; gRender(); return gSave(); }
    case 'after': { const r = await gRun('area_report', { box: G.box, hours: +$('#gAfterH').value }); if (!r) return; out('area report, after', r.summary); const p = r.data.report; G.after = { passes: p.passes, typical: p.typical, bad: p.bad, verdict: p.verdict }; G.afterPoints = r.data.points; gRender(); return gSave(); }
    case 'close': G.status = 'closed'; await gSave(); toast('Case closed. It stays in the list on the left.'); return;
  }
});
function gWaitBox() { const t = setInterval(() => { if (S.draw.mode !== 'box') { clearInterval(t); const b = S.draw.box; if (b) { G.box = [Math.min(b[0], b[2]), Math.min(b[1], b[3]), Math.max(b[0], b[2]), Math.max(b[1], b[3])].map(v => +v.toFixed(2)); S.draw.box = null; if (G.step === 0) { G.robot = $('#gRobot').value.trim(); } else if (G.step === 4) { G.candName = $('#gName').value.trim(); } gRender(); } } }, 200); }

// the step bar is shown only with the Guided tab
for (const b of $$('.tabs button')) b.addEventListener('click', () => { $('#gSteps').classList.toggle('hidden', b.dataset.tab !== 'guided'); if (b.dataset.tab === 'guided') gRender(); else { S.gEv = null; draw(); } });
function gStart() { G = gNew(); gCases(); gRender(); }
// start-up link used by the page test: ?gbox=x0,y0,x1,y1&gfrom=..&gto=..&grobot=..&gstep=3&gdrives=a,b&gvers=m1,m2
async function gAuto(q) {
  G = gNew(); G.box = q.get('gbox').split(',').map(Number); G.hours = 0; G.from = q.get('gfrom'); G.to = q.get('gto'); G.robot = q.get('grobot') || ''; G.map = S.A ? S.A.name : '';
  gRender(); await gFind();
  const want = +(q.get('gstep') || 2);
  if (want >= 3 && G.report) { gGo(2); }
  if (want >= 4 && G.report) { G.src = { loading: true }; gGo(3); await gSources(); if (q.get('gdrives')) { G.drives = q.get('gdrives').split(','); if (q.get('gvers')) G.vers = q.get('gvers').split(','); gRender(); await gEvidence(); } }
  if (want >= 5 && G.ev) gGo(4);
  window.__gReady = true;
}

// the region for the piece move is drawn with the polygon tool
function gWaitPoly() { toast('click the corners around the whole piece; double-click to finish'); const t = setInterval(() => { if (S.draw.mode !== 'poly') { clearInterval(t); if (S.draw.poly.length >= 3) { G.pc.region = S.draw.poly.map(q => [q[0], q[1]]); G.pc.proof = null; S.draw.poly = []; $('#region').value = ''; } gRender(); gSave(); } }, 200); }
// an armed recording: say what it is doing, and refresh the list when it has finished
function gArmWatch() { const t = setInterval(async () => { let d; try { d = (await run('record_status', {}, true)).data; } catch (e) { return; } if (!G) return clearInterval(t); if (d.phase) G.armed = d.robot + ': ' + d.phase + (d.running ? ' (' + d.stats.samples + ' samples)' : ''); if (!d.running) { clearInterval(t); await refresh(); if (G.step === 3) gSources(); } else if (G.step === 3 && !gBusy) { const el0 = document.querySelector('#tab-guided p.warn'); if (el0) el0.textContent = G.armed; } }, 3000); }
