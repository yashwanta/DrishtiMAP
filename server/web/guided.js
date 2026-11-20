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
  const e = S.gEv; if (!e) return; const sz = Math.max(2, Math.min(5, S.view.k / 10)) * dp;
  dots(e.seen, '#4ade80', sz); dots(e.unseen, '#f87171', sz); dots(e.always, '#60a5fa', sz); dots(e.sometimes, '#e879f9', sz);
}

function gRender() {
  if (!G) G = gNew();
  $('#gSteps').replaceChildren(...GSTEPS.map((s, k) => { const d = el('div', { className: 'st ' + (k === G.step ? 'on' : k <= G.reached ? 'done' : 'off') }, el('span', { className: 'n', textContent: k < G.step || (k <= G.reached && k !== G.step) ? '✓' : k + 1 }), s); if (k <= G.reached) d.onclick = () => gGo(k); return d; }));
  const p = $('#tab-guided'); p.innerHTML = [gS1, gS2, gS3, gS4, gS5, gS6, gS7, gS8][G.step](); p.scrollTop = 0;
  if (G.step === 0) { const m = $('#gMap'); m.replaceChildren(...allMaps().map(n => el('option', { value: n, textContent: n }))); m.value = G.map || (S.A && S.A.name) || m.value; G.map = m.value; m.onchange = () => { G.map = m.value; showMap(m.value); }; $('#gRobot').value = G.robot || ''; $('#gFrom').value = G.from || ''; $('#gTo').value = G.to || ''; }
  gOverlay();
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
  const e = G.ev, ds = S.ws.drives, others = allMaps().filter(n => n !== G.map);
  return `<h2>④ Evidence</h2><p>The history shows where and when. Laser recordings show which walls do not match. Choose recordings that drive through the box, made at different times if you have them.</p>
  <div class="gcard"><b>Recordings</b>${ds.length ? ds.map(d => `<label class="row"><input type="checkbox" class="gDrive" value="${esc(d.name)}"${(G.drives || []).includes(d.name) ? ' checked' : ''}>${esc(d.name)} <small class="dim">${d.samples}</small></label>`).join('') : '<p>None yet.</p>'}
   <div class="row"><select id="gRecRobot">${S.ws.robots.map(r => `<option>${esc(r.name)}</option>`).join('')}</select><button data-act="rec">Record a pass …</button></div><span class="dim">Recording only reads from the robot. Start it before the robot reaches the box.</span></div>
  <div class="gcard"><b>Compare with other map versions</b>${others.length ? others.map(n => `<label class="row"><input type="checkbox" class="gVer" value="${esc(n)}"${(G.vers || []).includes(n) ? ' checked' : ''}>${esc(n)}</label>`).join('') : '<p>No other version in this workspace.</p>'}</div>
  <div class="row"><button class="primary" data-act="evidence">Lay the lasers over the map</button></div>
  ${e ? `<div class="gcard"><b>How well each version fits here</b><table class="gt">${e.fits.map((f, i) => `<tr><td>${esc(f.map)}${i === 0 ? ' <span class="dim">(in question)</span>' : ''}</td><td class="${f.match >= 55 ? 'ok' : f.match >= 40 ? 'warn' : 'bad'}">${f.match.toFixed(0)} %</td></tr>`).join('')}</table></div>
   <div class="gcard"><b>${esc(G.map)} in this box</b><div class="gkv"><span><i style="background:#4ade80"></i>walls seen by the lasers</span><span>${e.seenPct.toFixed(0)} %</span><span><i style="background:#f87171"></i>walls not seen (gone, or hidden)</span><span>${e.unseenPct.toFixed(0)} %</span>
    <span><i style="background:#60a5fa"></i>seen ${e.drives > 1 ? 'in every recording' : ''}, not in the map</span><span>${e.always} cells</span>${e.drives > 1 ? `<span><i style="background:#e879f9"></i>seen in some recordings only</span><span>${e.sometimes} cells</span>` : ''}</div>
    ${e.drives < 2 ? '<span class="dim">With one recording a parked bin and a new wall look the same. A second recording at another time tells them apart.</span>' : ''}</div>` : ''}
  ${gNav('Fix options', !!e)}`;
}
async function gEvidence() {
  G.drives = $$('.gDrive:checked').map(c => c.value); G.vers = $$('.gVer:checked').map(c => c.value);
  if (!G.drives.length) return out('guided', 'Tick at least one recording that drives through the box, or record a pass.');
  const r = await gRun('box_evidence', { box: G.box, drives: G.drives, maps: [G.map, ...G.vers] }); if (!r) return;
  const d = r.data; G.evPts = { seen: f32(d.ptsSeen), unseen: f32(d.ptsUnseen), always: f32(d.ptsAlways), sometimes: f32(d.ptsSometimes) };
  G.ev = { fits: d.fits, bestOther: d.bestOther, seenPct: d.seenPct, unseenPct: d.unseenPct, always: d.always, sometimes: d.sometimes, drives: d.drives }; G.evText = r.summary;
  out('laser evidence', r.summary); gRender(); gSave();
}
// ---- 5
function gS5() {
  const e = G.ev || { fits: [] }, cur = e.fits[0] || { match: 0 }, bo = e.bestOther, bf = e.fits.find(f => f.map === bo);
  const comes = e.drives > 1 && e.sometimes > 3 * e.always && e.sometimes > 30;
  const opt = (id, on, title, text) => `<div class="gopt${on ? (G.fix === id ? ' sel' : '') : ' no'}"${on ? ` data-fix="${id}"` : ''}><span class="r"></span><span><b>${title}</b><br><span class="dim">${text}</span></span></div>`;
  return `<h2>⑤ Fix options</h2><p>Only fixes that fit the evidence can be chosen.</p>
  ${opt('donor', !!bo, bo ? 'Take the walls in the box from ' + esc(bo) : 'Take the walls in the box from another version', bo ? `It fits the lasers ${bf.match.toFixed(0)} % here; the map in question ${cur.match.toFixed(0)} %. Stations and paths stay where they are.` : 'Not offered: no other version in the comparison fits the lasers at least 5 points better. Tick more versions in step 4 if you have them.')}
  ${opt('piece', false, 'Turn or shift a whole piece of the map', 'Not in the guided steps yet. If Detect shows a jump at a seam, use the Advanced tab: Find rigid pieces, Measure turn, Build. A box cannot fix a misplaced piece; it only moves the jump.')}
  ${opt('rescan', false, 'Rescan this area in RoboShop', cur.match < 55 && !bo ? 'This is the way here: no version fits what the robots see (' + cur.match.toFixed(0) + ' %). The floor has changed since every scan.' : 'For when no version fits. Not needed if an option above can be chosen.')}
  ${comes ? '<div class="gcard"><b>Things come and go here</b><p>Most of what the lasers see outside the map is there in some recordings only. A storage position cannot be made right in a map: any version is wrong half the time.</p></div>' : ''}
  <div class="gcard"><b>Area to change</b><div class="row"><button data-act="draw">Redraw the box</button><span class="dim">x ${G.box[0].toFixed(1)}…${G.box[2].toFixed(1)}, y ${G.box[1].toFixed(1)}…${G.box[3].toFixed(1)}</span></div><label>Name of the new map file<input id="gName" value="${esc(G.candName || '')}" placeholder="PLANTMAP_v6"></label></div>
  ${gNav('Build', G.fix === 'donor')}`;
}
// ---- 6
function gS6() {
  const p = G.proof;
  if (!p) return `<h2>⑥ Build and prove</h2><p>Writes the new map as a file in <i>candidates</i>, runs every check, and lays the same recordings over it. The map on the robots is not touched.</p><div class="row"><button class="primary" data-act="build">Build ${esc(G.candName || '')}</button></div>${gNav('')}`;
  return `<h2>⑥ Build and prove</h2>
  <div class="gcard"><b>File</b><p>${esc(G.cand)}.smap in <i>candidates</i>. Not on any robot.</p></div>
  <div class="gcard verdict ${p.checksOk ? 'v-ok' : 'v-map'}"><b>Checks: ${p.checksOk ? 'all pass' : 'NOT all pass'}</b><p>${p.checksOk ? 'Same stations, paths, bins and areas; nothing closer to a wall than before; no feature line on a path.' : (p.wallFail ? 'With these walls a path or station would be too close to a wall. That is the sign of a misplaced piece of map: the walls belong somewhere else than the stations and paths that go with them, and a box cannot fix that. Go to the Advanced tab: Find rigid pieces, Measure turn, Build (move a region). ' : '') + 'The full report is in the output panel. Do not go on with a failed check.'}</p></div>
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
async function gRun(tool, args) {
  if (gBusy || busy) { toast('Another analysis is still running.'); return null; }
  gBusy = true; busy = true; for (const b of $$('#tab-guided button')) b.disabled = true;
  try { return await run(tool, args); } catch (e) { out(tool.replace(/_/g, ' ') + ' — did not run', e.message); return null; }
  finally { gBusy = false; busy = false; for (const b of $$('#tab-guided button')) b.disabled = false; }
}
$('#tab-guided').addEventListener('click', async ev => {
  const t = ev.target.closest('[data-act],[data-hours],[data-fix]'); if (!t || t.disabled) return;
  if (t.dataset.hours !== undefined) { G.hours = +t.dataset.hours; G.robot = $('#gRobot').value.trim(); G.from = $('#gFrom').value; G.to = $('#gTo').value; return gRender(); }
  if (t.dataset.fix) { G.fix = t.dataset.fix; G.candName = $('#gName').value.trim(); return gRender(); }
  switch (t.dataset.act) {
    case 'back': return gGo(G.step - 1);
    case 'next': if (G.step === 4) { G.candName = $('#gName').value.trim(); if (!G.candName) return out('guided', 'Give the new map file a name.'); G.proof = null; } return gGo(G.step + 1);
    case 'tofix': return gGo(4);
    case 'draw': if (!S.A) return out('guided', 'Choose a map first.'); S.draw.box = null; setMode('box'); toast('drag a box on the map'); gWaitBox(); return;
    case 'site': return $('#histSite').click();
    case 'find': return gFind();
    case 'rec': return recordDlg($('#gRecRobot').value);
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
  if (want >= 4 && G.report) { gGo(3); if (q.get('gdrives')) { G.drives = q.get('gdrives').split(','); G.vers = (q.get('gvers') || '').split(',').filter(Boolean); gRender(); await gEvidence(); } }
  if (want >= 5 && G.ev) gGo(4);
}
