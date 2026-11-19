// End-to-end test of a running DRISHTI Map server:  node tests/e2e.js <url-with-token> [step ...]
// Runs the real tools on the workspace's Springfield data and checks the known results.
const url = new URL(process.argv[2]);
const only = process.argv.slice(3);
const base = url.origin, token = url.searchParams.get('t');
const H = { 'X-DM-Token': token, 'Content-Type': 'application/json' };
let failed = 0;
const ok = (c, m) => { console.log((c ? 'PASS  ' : 'FAIL  ') + m); if (!c) failed++; };
async function run(tool, args) {
  const r = await fetch(base + '/api/run', { method: 'POST', headers: H, body: JSON.stringify({ tool, args }) });
  const j = await r.json(); if (!r.ok) throw new Error(j.error);
  for (;;) {
    await new Promise(s => setTimeout(s, 200));
    const st = await (await fetch(base + '/api/job?id=' + j.job, { headers: H })).json();
    if (st.state === 'running') continue;
    if (st.state !== 'done') throw new Error(st.error);
    return Object.assign(st.result, { seconds: st.seconds });
  }
}
const LIVE = 'SPRAMRMAP_live_421395c7', V5 = 'SPRAMRMAP_v5', D6 = 'amr06_loop_nov13';
const REGION = [[7.5, -14], [7.5, 1], [12, 3], [12, 29], [15, 31], [15, 42], [500, 42], [500, -14]];
const steps = {
  async security() {
    const a = await fetch(base + '/api/state'); ok(a.status === 403, 'API without the session token is refused (HTTP ' + a.status + ')');
    const b = await fetch(base + '/api/state', { headers: { ...H, Host: 'evil.example' } }); ok(b.status === 200 || b.status === 403, 'request answered');
    const c = await fetch(base + '/api/run', { method: 'POST', headers: H, body: JSON.stringify({ tool: 'map_info', args: { map: '..\\..\\secret' } }) });
    const cj = await c.json(); let err = ''; if (cj.job) { for (;;) { const st = await (await fetch(base + '/api/job?id=' + cj.job, { headers: H })).json(); if (st.state !== 'running') { err = st.error || ''; break; } } }
    ok(/not allowed/.test(err), 'a map name that tries to leave the workspace is refused: ' + err);
  },
  async workspace() {
    const r = await run('list_workspace', {}); console.log(r.summary);
    ok(r.data.maps.length >= 6 && r.data.drives.length >= 3, 'workspace lists maps and recordings');
    const i = await run('map_info', { map: LIVE }); ok(/421395c7/.test(i.summary) && i.data.stations === 394, 'map details: live map md5 and 394 stations');
    const s = await run('stations', { map: LIVE, names: ['LM443', 'LM521'] }); console.log(s.summary); ok(/LM443/.test(s.summary), 'stations lookup');
  },
  async summary() {
    const r = await run('drive_summary', { drive: 'amr03_v5_trial_nov17', from: 1, to: 1800 }); console.log(r.summary.split('\n').slice(0, 6).join('\n'));
    ok(r.data.samples === 1800 && r.data.track.length === 1800, 'recording summary reads 1800 samples (' + r.seconds.toFixed(1) + ' s)');
  },
  async replay() {
    const t = await run('laser_track', { drive: D6 }); console.log(t.summary);
    const a = await run('replay', { drive: D6, map: LIVE }); console.log(a.summary);
    ok(/JUMPS[^\n]*: 1\n\s+near LM52[01]/.test(a.summary), 'live map: one jump, at LM520/LM521 (' + a.seconds.toFixed(1) + ' s)');
    const b = await run('replay', { drive: D6, map: V5 }); console.log(b.summary);
    ok(/JUMPS[^\n]*: 0/.test(b.summary), 'v5: no jump');
    ok(b.data.rows.length === 426 && b.data.cloud.length > 1000, 'replay returns drawing data');
    const f = await run('rigid_fit', { drive: D6, map: V5, ranges: [[1, 370]] }); console.log(f.summary.split('\n').slice(0, 2).join('\n'));
    ok(/agrees with the lasers/.test(f.summary), 'v5 fits the drive as one rigid track');
    const g = await run('rigid_fit', { drive: D6, map: LIVE, ranges: [[1, 370]] }); console.log(g.summary.split('\n').slice(0, 2).join('\n'));
    ok(/NOT one rigid piece/.test(g.summary), 'live map does not fit as one rigid track');
  },
  async diagnose() {
    const p = await run('find_pieces', { drive: D6, map: LIVE }); console.log(p.summary);
    ok(p.data.some(x => x.stepAtSeam > 1.2), 'find_pieces shows a seam of more than 1.2 m on the live map');
    const d = await run('drift', { drive: D6, map: LIVE, ranges: [[40, 262]] }); console.log(d.summary.split('\n').slice(-3).join('\n'));
    const m = await run('measure_turn', { drive: D6, pieceMap: LIVE, pieceRanges: [[40, 262]], refMap: 'SPRAMRMAP_v3_orig', refRanges: [[172, 372]], stations: ['LM430', 'LM443', 'LM513', 'LM521'] });
    console.log(m.summary); ok(Math.abs(m.data.rotDeg - 2.19) < 0.25, 'measured turn ' + m.data.rotDeg.toFixed(3) + ' deg (+-' + m.data.spreadDeg.toFixed(2) + ') agrees with the 2.19 deg used for v5 (' + m.seconds.toFixed(0) + ' s)');
    steps.T = m.data;
  },
  async build() {
    for (const n of ['E2E_move', 'E2E_final', 'E2E_nolines']) await fetch(base + '/api/delete-candidate', { method: 'POST', headers: H, body: JSON.stringify({ name: n }) });
    const T = { th: 0.038176525363106915, tx: -0.13739245063794822, ty: -0.12705210082579033 };
    const a = await run('build_move_region', { base: LIVE, name: 'E2E_move', ...T, region: REGION, anchor: 'SPRAMRMAP_sep16' }); console.log(a.summary);
    ok(/stations 37 /.test(a.summary) && /ALL CHECKS PASS/.test(a.summary), 'move region: 37 stations moved, all checks pass');
    const b = await run('build_merge_box', { base: 'E2E_move', donor: 'SPRAMRMAP_v3_orig', name: 'E2E_final', x0: 9, x1: 31, y0: 29.5, y1: 42 });
    ok(/removed 10766, added 12976/.test(b.summary), 'merge box: same wall counts as the original v5 build');
    const c = await run('check_candidate', { base: LIVE, candidate: 'E2E_final' }); console.log(c.summary); ok(c.data.allOk, 'rebuilt candidate passes every check against the live map');
    const d = await run('compare_maps', { a: V5, b: 'E2E_final' }); console.log(d.summary);
    ok(/stations at a different position/.test(d.summary) === false && /wall dots only in A: 0, only in B: 0/.test(d.summary), 'rebuilt candidate has the same stations and walls as the v5 that ran on AMR-03');
    const e = await run('check_candidate', { base: LIVE, candidate: V5 }); ok(!e.data.allOk && e.data.nearLines.some(n => n.path === 'LM514-LM515'), 'the check catches the feature line left on LM514-LM515 in the delivered v5');
    const f = await run('build_remove_lines', { base: V5, name: 'E2E_nolines', fromCheck: true, checkAgainst: LIVE }); ok(/ALL CHECKS PASS/.test(f.summary), 'removing the reported line from v5 gives a file that passes: ' + f.summary.split('\n')[2]);
    const g = await run('replay', { drive: D6, map: 'E2E_final' }); ok(/JUMPS[^\n]*: 0/.test(g.summary), 'replay through the rebuilt candidate: no jump');
    const dup = await run('build_merge_box', { base: 'E2E_move', donor: 'SPRAMRMAP_v3_orig', name: 'E2E_final', x0: 9, x1: 31, y0: 29.5, y1: 42 }).then(() => '', e => e.message); ok(/already exists/.test(dup), 'a candidate is never overwritten: ' + dup);
    const src = await run('build_merge_box', { base: 'E2E_move', donor: 'SPRAMRMAP_v3_orig', name: LIVE, x0: 9, x1: 31, y0: 29.5, y1: 42 }).then(() => '', e => e.message); ok(/already exists/.test(src), 'a source map name cannot be used for a candidate: ' + src);
    for (const n of ['E2E_move', 'E2E_final', 'E2E_nolines']) await fetch(base + '/api/delete-candidate', { method: 'POST', headers: H, body: JSON.stringify({ name: n }) });
  },
  async robot() {
    const before = (await run('list_workspace', {})).data.robots || [];
    await fetch(base + '/api/robots', { method: 'POST', headers: H, body: JSON.stringify([{ name: 'TEST-unreachable', ip: '192.0.2.1' }]) });
    const e = await run('robot_status', { robot: 'TEST-unreachable' }).then(() => '', x => x.message); ok(/cannot read/.test(e), 'unreachable robot gives a clear error: ' + e.slice(0, 90));
    const r = await run('record_start', { robot: 'TEST-unreachable', name: 'e2e_rec' }).then(() => '', x => x.message); ok(/cannot reach/.test(r), 'recording refuses to start when the robot is unreachable');
    await fetch(base + '/api/robots', { method: 'POST', headers: H, body: JSON.stringify(before) });   // put the user's robot list back
  },
};
(async () => {
  for (const [n, f] of Object.entries(steps)) { if (typeof f !== 'function' || (only.length && !only.includes(n))) continue; console.log('\n===== ' + n); try { await f(); } catch (e) { ok(false, n + ' threw: ' + e.message); } }
  console.log(failed ? `\n${failed} FAILED` : '\nALL END-TO-END CHECKS PASS'); process.exit(failed ? 1 : 0);
})();
