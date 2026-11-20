// Page script for tests\ui.js: the guided piece move on the reference case (a loop attached about 2 degrees turned).
//   node tests\ui.js <address> shot.png "map=<live map>&gbox=16,29.5,31,42&gfrom=..&gto=..&gstep=5&gdrives=amr06_loop_nov13" @tests\guided_piece.js
// Reports through window.__done: the pieces found, the measured turn, the checks and the jumps before and after.
window.__run = (async () => {
  const w = ms => new Promise(r => setTimeout(r, ms));
  const panel = () => document.querySelector('#tab-guided').textContent;
  const click = s => { const e = document.querySelector(s); if (!e) throw new Error('no element ' + s + ' at step ' + G.step + ': ' + panel().slice(0, 200)); if (e.disabled) throw new Error('disabled ' + s + ': ' + panel().slice(0, 200)); e.click(); };
  const until = async (f, what, n = 900) => { for (let i = 0; i < n; i++) { if (f()) return; await w(500); } throw new Error('timed out waiting for ' + what + '; output: ' + document.querySelector('#out').textContent.slice(0, 300)); };
  if (G.step !== 4) return { stoppedAt: G.step, why: 'not at the fix options', panel: panel().slice(0, 200) };
  const jumps = (G.jumps || []).map(j => j.step.toFixed(2) + ' m near ' + j.near);
  document.querySelector('#gName').value = 'GUIDED_piece_test';
  click('[data-fix=piece]'); await w(300); click('[data-act=next]'); await w(400);
  click('[data-act=pieces]'); await until(() => G.pc && G.pc.pieces, 'the rigid pieces');
  const pieces = G.pc.pieces.map(p => p.range.From + '-' + p.range.To + ' ' + p.from + '..' + p.to + ' step ' + p.stepAtSeam.toFixed(2) + ' turn ' + p.turnDeg.toFixed(1));
  const guess = { move: G.pc.move, ref: G.pc.ref };
  // the loop (samples up to about 262) is the misplaced piece; the stretch after sample 300 is the part that is right
  for (const c of document.querySelectorAll('.gPcMove')) c.checked = G.pc.pieces[+c.value].range.To <= 290 && G.pc.pieces[+c.value].range.To >= 60;
  for (const c of document.querySelectorAll('.gPcRef')) c.checked = G.pc.pieces[+c.value].range.From >= 295;
  click('[data-act=measure]'); await until(() => G.pc.turn, 'the measured turn');
  const turn = G.pc.turn;
  G.pc.region = [[7.5, -14], [7.5, 1], [12, 3], [12, 29], [15, 31], [15, 42], [500, 42], [500, -14]];
  G.pc.anchor = [...document.querySelectorAll('#gPcAnchor option')].map(o => o.value).find(v => v.endsWith('_sep16')) || '';
  gRender(); await w(300); document.querySelector('#gPcAnchor').value = G.pc.anchor;
  click('[data-act=pbuild]'); await until(() => G.pc.proof && panel().includes('Checks:'), 'the build');
  await w(2500);
  // the move alone leaves the seam: repair it with the best-fitting walls, as the reference fix did
  let seam = null;
  if (G.pc.proof.checksOk && G.pc.proof.jumpsAfter > 0) {
    const left = G.pc.afterJumps.map(j => j.step.toFixed(2) + ' m near ' + j.near), box = G.pc.seamBox;
    click('[data-act=seamfind]'); await until(() => G.pc.seamFits, 'the walls for the seam');
    const fits = G.pc.seamFits.map(f => f.map.slice(-12) + ' ' + f.match.toFixed(0)), donor = G.pc.seamDonor;
    if (donor) { await w(400); click('[data-act=seambuild]'); await until(() => G.pc.seam && panel().includes('repaired'), 'the seam repair'); await w(2500); }
    seam = { left, box, fits, donor: donor.slice(-10), result: G.pc.seam };
  }
  const next = document.querySelector('[data-act=next]');
  return { jumps, pieces, guess, seam, turn: { rotDeg: +turn.rotDeg.toFixed(2), spread: +turn.spreadDeg.toFixed(2), tx: +turn.tx.toFixed(3), ty: +turn.ty.toFixed(3) }, anchor: G.pc.anchor.slice(-6), proof: G.pc.proof, cand: G.cand, canGoToTrial: !!next && !next.disabled };
})().then(r => { window.__done = r || 'empty'; }, e => { window.__done = 'ERROR ' + e.message; });
'started';
