// End-to-end test of detect, hand edits, help and the fleet check on a running DRISHTI Map server:
//   node tests/e2e_edit.js <url-with-token> [step ...]
const url = new URL(process.argv[2]);
const only = process.argv.slice(3);
const base = url.origin, H = { 'X-DM-Token': url.searchParams.get('t'), 'Content-Type': 'application/json' };
let failed = 0;
const ok = (c, m) => { console.log((c ? 'PASS  ' : 'FAIL  ') + m); if (!c) failed++; };
const post = async (p, body) => { const r = await fetch(base + p, { method: 'POST', headers: H, body: JSON.stringify(body) }); const j = await r.json(); if (!r.ok) throw new Error(j.error); return j; };
const get = async p => { const r = await fetch(base + p, { headers: H }); const j = await r.json(); if (!r.ok) throw new Error(j.error); return j; };
async function run(tool, args) {
  const j = await post('/api/run', { tool, args });
  for (;;) {
    await new Promise(s => setTimeout(s, 200));
    const st = await get('/api/job?id=' + j.job);
    if (st.state === 'running') continue;
    if (st.state !== 'done') throw new Error(st.error);
    return Object.assign(st.result, { seconds: st.seconds });
  }
}
const del = n => fetch(base + '/api/delete-candidate', { method: 'POST', headers: H, body: JSON.stringify({ name: n }) });
const LIVE = 'SPRAMRMAP_live_421395c7', V5 = 'SPRAMRMAP_v5', EDIT = 'SPRAMRMAP_v5_robot_edit_1215', D6 = 'amr06_loop_nov13';
const steps = {
  async detect() {
    const a = await run('detect_problems', { map: V5 }); console.log(a.summary);
    ok(a.data.findings.some(f => f.kind === 'line_on_path' && f.level === 'fault' && /LM51[45]-LM51[45]/.test(f.text)), 'map-only detect flags the feature line on LM514-LM515 in v5 as a fault');
    const b = await run('detect_problems', { map: LIVE }); ok(!b.data.findings.some(f => f.level === 'fault'), 'map-only detect reports no fault in the live map file (its fault needs a recording): ' + b.summary.split('\n')[2]);
    const c = await run('detect_problems', { map: LIVE, drive: D6 }); console.log(c.summary);
    ok(c.data.findings.some(f => f.kind === 'jump' && /LM52[01]/.test(f.near)) && c.data.findings.some(f => f.kind === 'not_rigid'), 'detect with the recording: jump at LM520/LM521 and "not one rigid track" on the live map (' + c.seconds.toFixed(1) + ' s)');
    const d = await run('detect_problems', { map: V5, drive: D6 }); console.log(d.summary.split('\n').slice(0, 4).join('\n'));
    ok(!d.data.findings.some(f => f.kind === 'jump' || f.kind === 'not_rigid') && d.data.rms < 0.15, 'detect with the recording on v5: no jump, rigid fit ' + d.data.rms.toFixed(3) + ' m');
  },
  async edit() {
    for (const n of ['E2E_edit', 'E2E_edit2']) await del(n);
    // the window's edit session: the two edits made by hand on the robot
    const s = await post('/api/edit/start', { base: V5 }); ok(s.edit.base === V5 && s.view.stations.length === 394 && s.view.pathInfo.length === 807, 'edit session starts on a copy of v5');
    const lines0 = s.view.lines.length;
    const bad = await post('/api/edit/op', { op: 'set_path', path: 'LM517-LM518', key: 'direction', value: 'fast' }).then(() => '', e => e.message); ok(/whole number/.test(bad), 'a wrong value is refused: ' + bad);
    let r = await post('/api/edit/op', { op: 'delete_lines', at: [36.24, 35.25] }); console.log('  ' + r.note); ok(r.view.lines.length < lines0 && /36.16,35.26/.test(r.note), 'feature line at LM514-LM515 deleted by clicking it');
    r = await post('/api/edit/op', { op: 'set_path', paths: ['LM517-LM518', 'LM518-LM519', 'LM519-LM520', 'LM520-LM521'], key: 'direction', value: 0 }); console.log('  ' + r.note);
    ok(/direction=0/.test(r.view.pathInfo[r.view.pathNames.indexOf('LM517-LM518')]), 'paths LM517..LM521 set to forward');
    r = await post('/api/edit/op', { op: 'move_station', station: 'LM430', dx: 0.5, dy: 0 }); ok(r.edit.log.length === 3, 'third edit applied');
    r = await post('/api/edit/undo', {}); ok(r.edit.log.length === 2 && r.view.stations.find(x => x.n === 'LM430').x === s.view.stations.find(x => x.n === 'LM430').x, 'undo puts the station back');
    const sv = await post('/api/edit/save', { name: 'E2E_edit' }); console.log(sv.summary);
    ok(/ALL CHECKS PASS/.test(sv.summary) && /changed by hand/.test(sv.summary) && /2 hand edit/.test(sv.summary), 'saved as a candidate; report lists the edits and every check passes');
    const st = await get('/api/edit/state'); ok(st.edit === null, 'session closed after saving');
    const c = await run('compare_maps', { a: EDIT, b: 'E2E_edit' }); console.log(c.summary);
    ok(!/direction/.test(c.summary), 'path directions equal the file edited on the robot');
    const d = await run('detect_problems', { map: 'E2E_edit' }); ok(!d.data.findings.some(f => f.level !== 'info'), 'detector finds nothing on the edited candidate');
    const p = await run('replay', { drive: D6, map: 'E2E_edit' }); ok(/JUMPS[^\n]*: 0/.test(p.summary), 'replay through the edited candidate: no jump');
    // the same through the assistant's tool
    const t = await run('build_edit', { base: V5, name: 'E2E_edit2', edits: [{ op: 'delete_lines', at: [36.24, 35.25] }, { op: 'set_path', region: [[26, 34.5], [36.5, 34.5], [36.5, 36], [26, 36]], key: 'direction', value: 0 }, { op: 'erase_walls', box: [60, 60, 61, 61] }] }).then(x => x.summary, e => 'ERR ' + e.message);
    ok(/no wall dots inside|no file written/.test(t), 'build_edit stops at the first edit that cannot be applied and writes nothing: ' + t.slice(0, 120));
    const u = await run('build_edit', { base: V5, name: 'E2E_edit2', edits: [{ op: 'delete_lines', at: [36.24, 35.25] }, { op: 'set_path', paths: ['LM517-LM518'], key: 'maxspeed', value: 0.4 }] });
    ok(/ALL CHECKS PASS/.test(u.summary) && /maxspeed = 0.4/.test(u.summary), 'build_edit writes a checked candidate');
    const dup = await run('build_edit', { base: V5, name: 'E2E_edit2', edits: [{ op: 'delete_lines', at: [36.24, 35.25] }] }).then(() => '', e => e.message); ok(/already exists/.test(dup), 'a candidate is never overwritten');
    for (const n of ['E2E_edit', 'E2E_edit2']) await del(n);
  },
  async help() {
    const l = await get('/api/help'); console.log(l.map(a => a.id + '  ' + a.title).join('\n'));
    ok(l.length === 20 && l[0].id === 'README' && l.filter(a => a.group === 'Work instructions').length === 10 && l.filter(a => a.group === 'Knowledge base').length === 9, 'help lists the start page, 10 work instructions and 9 knowledge-base articles');
    const links = new Set(); let empty = 0;
    for (const a of l) { const t = (await get('/api/help?id=' + a.id)).text; if (t.length < 400) empty++; for (const m of t.matchAll(/\[\[([A-Z]+-?\d*)\]\]/g)) links.add(m[1]); }
    const ids = new Set(l.map(a => a.id)); const broken = [...links].filter(x => !ids.has(x));
    ok(empty === 0 && broken.length === 0, 'every article has content and every link points to an article' + (broken.length ? ': broken ' + broken : ''));
    const h = await run('help', { topic: 'WI-06' }); ok(/Delete line/.test(h.summary), 'the assistant can read a work instruction');
    const tools = (await get('/api/state')).tools.map(t => t.name); ok(['detect_problems', 'build_edit', 'fleet_maps', 'help'].every(t => tools.includes(t)), 'new tools are offered to the assistant (' + tools.length + ' tools)');
  },
  async fleet() {
    const before = (await run('list_workspace', {})).data.robots || [];
    await post('/api/robots', [{ name: 'T-1', ip: '192.0.2.1' }, { name: 'T-2', ip: '192.0.2.2' }]);
    try {
      const r = await run('fleet_maps', {}); console.log(r.summary);
      ok(r.data.rows.length === 2 && r.data.rows.every(x => !x.ok) && /No robot answered/.test(r.summary), 'fleet check lists unreachable robots and says so (' + r.seconds.toFixed(0) + ' s)');
    } finally { await post('/api/robots', before); }
    const after = (await run('list_workspace', {})).data.robots || []; ok(after.length === before.length, 'robot list restored (' + after.length + ')');
  },
};
(async () => {
  for (const [n, f] of Object.entries(steps)) { if (only.length && !only.includes(n)) continue; console.log('\n===== ' + n); try { await f(); } catch (e) { ok(false, n + ' threw: ' + e.message); } }
  console.log(failed ? '\n' + failed + ' FAILED' : '\nALL END-TO-END CHECKS PASS'); process.exit(failed ? 1 : 0);
})();
