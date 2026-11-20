// Page script for tests\ui.js: the Evidence step reading laser scans stored by an AMR Health server.
// Run the stand-in first (it serves real recorded laser replies) on a workspace whose site.json names it,
// and store the sign-in (tester / tester):
//   node tests\health_standin.js 47901 <workspace>\drives\<recording> 4
//   node tests\ui.js <address> shot.png "map=<map>" @tests\guided_scans.js
window.__run = (async () => {
  const w = ms => new Promise(r => setTimeout(r, ms));
  const until = async (f, what, n = 240) => { for (let i = 0; i < n; i++) { if (f()) return; await w(500); } throw new Error('timed out waiting for ' + what + '; output: ' + document.querySelector('#out').textContent.slice(0, 300)); };
  await until(() => S.A, 'the map to be shown');
  G = gNew(); G.map = S.A.name; G.box = [0, 30, 8, 38]; G.report = { robots: [], passes: 0, fine: 0, low: 0, bad: 0, typical: 0, verdict: 'test', rating: 'LOW', summary: '', isMap: true };
  G.reached = 3; G.step = 3; gRender();
  await until(() => G.scans, 'the stored scans to be listed by default');
  const listed = { total: G.scans.total, robots: G.scans.robots.map(r => r.robot + ' ' + r.scans) };
  await w(600); document.querySelector('[data-act=scanev]').click();
  await until(() => G.scanEv, 'the stored scans to be placed on the map');
  S.view.cx = 4; S.view.cy = 34; S.view.k = 45; draw(); await w(800);
  return { listed, robots: G.scanEv.robots.map(r => r.robot + ' ' + r.scans + ' scans ' + r.match.toFixed(0) + '%'), seenPct: +G.ev.seenPct.toFixed(0), always: G.ev.always, sometimes: G.ev.sometimes, canGoOn: !document.querySelector('[data-act=next]').disabled, jumpsNote: document.querySelector('#tab-guided').textContent.includes('Position jumps: not checked') };
})().then(r => { window.__done = r || 'empty'; }, e => { window.__done = 'ERROR ' + e.message; });
'started';
