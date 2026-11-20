// Page script for tests\ui.js: a brand-new user. Start the program on an EMPTY workspace with a site
// file (drishti-site.json) beside the exe and nothing else set up. It must join the site by itself,
// fill in the robots, copy the map, and give an area report without any login.
//   node tests\ui.js <address> shot.png "" @tests\guided_newuser.js
window.__run = (async () => {
  const w = ms => new Promise(r => setTimeout(r, ms));
  const until = async (f, what, n = 360) => { for (let i = 0; i < n; i++) { if (f()) return; await w(500); } throw new Error('timed out waiting for ' + what + '; output: ' + document.querySelector('#out').textContent.slice(0, 300)); };
  await until(() => gSiteData && gSiteData.rows && gSiteData.rows.length > 1 && !gBusy, 'the site to be set up');
  const site = { plant: gSiteData.plant, rows: gSiteData.rows.map(r => (r.ok ? 'ok ' : 'NO ') + r.item) };
  await until(() => S.A && S.ws.robots.length > 0, 'robots and a map');
  const got = { robots: S.ws.robots.length, map: !!S.A };
  // the area report without any fleet manager login: a box over the stations in the middle of the map
  const st = S.A.v.stations, mid = st[Math.floor(st.length / 2)];
  G.box = [mid.x - 6, mid.y - 6, mid.x + 6, mid.y + 6]; G.hours = 48; gRender(); await w(300);
  document.querySelector('[data-act=find]').click();
  await until(() => G.report, 'the area report');
  return { site, got, report: { coarse: !!G.report.coarse, verdict: G.report.verdict, rating: G.report.rating, robots: G.report.robots.length }, step: G.step };
})().then(r => { window.__done = r || 'empty'; }, e => { window.__done = 'ERROR ' + e.message; });
'started';
