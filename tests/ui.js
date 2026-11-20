// Window test: opens the real page in headless Edge, waits for it to finish, saves a screenshot and
// reports any script error.   node tests/ui.js <url-with-token> <shot.png> [query] [js-to-run-after-load]
const { spawn } = require('child_process');
const fs = require('fs'), path = require('path'), os = require('os');
const [url, shot, query = '', after = ''] = process.argv.slice(2);
const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const prof = path.join(os.tmpdir(), 'dm_edge_' + process.pid), port = 9300 + (process.pid % 500);
const edge = spawn(EDGE, ['--headless=new', '--disable-gpu', '--no-first-run', '--remote-debugging-port=' + port, '--user-data-dir=' + prof, '--window-size=1560,960', 'about:blank'], { stdio: 'ignore' });
const sleep = ms => new Promise(r => setTimeout(r, ms));
const done = code => { try { edge.kill(); } catch (e) { } setTimeout(() => { try { fs.rmSync(prof, { recursive: true, force: true }); } catch (e) { } process.exit(code); }, 600); };
(async () => {
  let tabs;
  for (let i = 0; i < 60; i++) { try { tabs = await (await fetch('http://127.0.0.1:' + port + '/json')).json(); if (tabs.length) break; } catch (e) { } await sleep(250); }
  const page = tabs.find(t => t.type === 'page');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise(r => ws.onopen = r);
  let id = 0; const wait = new Map(), errors = [];
  ws.onmessage = m => {
    const d = JSON.parse(m.data);
    if (d.id && wait.has(d.id)) { wait.get(d.id)(d.result || d); wait.delete(d.id); }
    if (d.method === 'Runtime.exceptionThrown') errors.push(d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text);
    if (d.method === 'Runtime.consoleAPICalled' && d.params.type === 'error') errors.push(d.params.args.map(a => a.value || a.description).join(' '));
    if (d.method === 'Log.entryAdded' && d.params.entry.level === 'error') errors.push(d.params.entry.text + ' ' + (d.params.entry.url || ''));
  };
  const send = (method, params = {}) => new Promise(r => { wait.set(++id, r); ws.send(JSON.stringify({ id, method, params })); });
  const evalJS = async e => (await send('Runtime.evaluate', { expression: e, awaitPromise: true, returnByValue: true })).result?.value;
  await send('Runtime.enable'); await send('Log.enable'); await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1560, height: 960, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: url + (query ? '&' + query : '') });
  const wantReady = /run=|hist=/.test(query);
  let title = '';
  for (let i = 0; i < 480; i++) { await sleep(250); title = await evalJS('document.title'); if (wantReady ? title === 'DRISHTI Map ready' : (title === 'DRISHTI Map' && await evalJS("!!document.querySelector('#mapsList li')"))) break; }
  if (after) { console.log('after:', JSON.stringify(await evalJS(after))); await sleep(1500); }
  await sleep(700);
  const info = await evalJS(`JSON.stringify({title:document.title, out:document.querySelector('#outTitle').textContent, outText:document.querySelector('#out').textContent.slice(0,300), maps:document.querySelectorAll('#mapsList li').length, drives:document.querySelectorAll('#driveList li').length, legend:document.querySelector('#legend').textContent, chat:document.querySelector('#chatLog').textContent.slice(0,600)})`);
  console.log(info);
  const png = await send('Page.captureScreenshot', { format: 'png' });
  fs.mkdirSync(path.dirname(shot), { recursive: true }); fs.writeFileSync(shot, Buffer.from(png.data, 'base64'));
  console.log('screenshot', shot, fs.statSync(shot).size, 'bytes');
  console.log(errors.length ? 'SCRIPT ERRORS:\n  ' + errors.join('\n  ') : 'no script errors');
  done(errors.length ? 1 : 0);
})().catch(e => { console.error('ui test failed:', e.message); done(2); });
setTimeout(() => { console.error('ui test timed out'); done(3); }, 170000);
