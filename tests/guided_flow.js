// Page script for tests\ui.js: drives the guided steps from Fix options through Build to Trial.
//   node tests\ui.js <address> shot.png "map=<map>&gbox=x0,y0,x1,y1&gfrom=..&gto=..&gstep=5&gdrives=<recording>&gvers=<other maps>" @tests\guided_flow.js
// It reports through window.__done. A box fix that would put a wall on a path must stop at Build.
window.__run = (async () => {
  const w = ms => new Promise(r => setTimeout(r, ms));
  const panel = () => document.querySelector('#tab-guided').textContent;
  const click = s => { const e = document.querySelector(s); if (!e) throw new Error('no element ' + s + ' at step ' + G.step + ': ' + panel().slice(0, 200)); e.click(); };
  if (G.step !== 4) return { stoppedAt: G.step, why: 'no evidence or no report', panel: panel().slice(0, 200) };
  const fits = G.ev.fits.map(f => f.map.slice(-10) + ' ' + f.match.toFixed(0));
  if (!G.ev.bestOther) return { stoppedAt: 4, why: 'no version fits better: nothing to build', fits };
  document.querySelector('#gName').value = 'GUIDED_e2e_test';
  click('[data-fix=donor]'); await w(300); click('[data-act=next]'); await w(400);
  click('[data-act=build]');
  for (let i = 0; i < 600 && !(G.proof && panel().includes('Checks:')); i++) await w(500);
  if (!G.proof) return { stoppedAt: 5, why: 'build did not finish', out: document.querySelector('#out').textContent.slice(0, 300) };
  await w(3000);
  const next = document.querySelector('[data-act=next]');
  if (!next || next.disabled) return { stoppedAt: 5, why: 'build step refuses to go on', proof: G.proof, fits, cand: G.cand };
  next.click(); await w(400);
  const drives = [...document.querySelectorAll('#gTrialDrive option')].map(o => o.value);
  document.querySelector('#gTrialDrive').value = drives.find(d => d.includes('trial')) || drives[0];
  click('[data-act=trialjudge]'); for (let i = 0; i < 120 && !G.trial; i++) await w(500);
  return { stoppedAt: G.step, proof: G.proof, fits, cand: G.cand, trial: G.trial && { pass: G.trial.pass, min: G.trial.min, jumps: G.trial.jumps } };
})().then(r => { window.__done = r || 'empty'; }, e => { window.__done = 'ERROR ' + e.message; });
'started';
