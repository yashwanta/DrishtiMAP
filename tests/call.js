// Run one tool on a running DRISHTI Map server:  node tests/call.js <url-with-token> <tool> '<json args>'
const url = new URL(process.argv[2]), H = { 'X-DM-Token': url.searchParams.get('t'), 'Content-Type': 'application/json' };
(async () => {
  if (process.argv[3] === 'robots') { const r = await fetch(url.origin + '/api/robots', { method: 'POST', headers: H, body: process.argv[4] }); console.log(await r.text()); return; }
  const r = await fetch(url.origin + '/api/run', { method: 'POST', headers: H, body: JSON.stringify({ tool: process.argv[3], args: JSON.parse(process.argv[4] || '{}') }) });
  const j = await r.json(); if (!r.ok) { console.log('ERROR', j.error); process.exit(1); }
  for (;;) {
    await new Promise(s => setTimeout(s, 250));
    const st = await (await fetch(url.origin + '/api/job?id=' + j.job, { headers: H })).json();
    if (st.state === 'running') continue;
    console.log(st.state === 'done' ? st.result.summary : 'ERROR ' + st.error); console.log('(' + st.seconds.toFixed(1) + ' s)'); process.exit(st.state === 'done' ? 0 : 1);
  }
})();
