'use strict';
// LiDAR scan viewer: one scan at a time from a recording, or live from a robot (read-only).
// Each scanner is drawn in its own colour with the height of its scan plane. Uses the globals of app.js.
const SCANC = ['#fb923c', '#c084fc', '#34d399', '#f472b6'];
let scanLiveT = 0, scanBusy = false;

function paintScan(dp) {
  const sc = S.ov.scan; if (!sc || !S.layers.lasers) return;
  const k = S.view.k, v = sc.v, sz = Math.max(2, Math.min(5, k / 10)) * dp;
  sc.P.forEach((a, i) => dots(a, SCANC[i % SCANC.length], sz));
  // the robot: body outline from the scanner positions, heading arrow, each scanner as a dot
  const th = v.deg * Math.PI / 180, c = Math.cos(th), s = Math.sin(th), W = (x, y) => [X(v.x + c * x - s * y), Y(v.y + s * x + c * y)];
  const xs = v.lasers.map(l => l.x), front = Math.max(0.35, ...xs) + 0.05, back = Math.min(-0.35, ...xs) - 0.05, half = 0.32;
  ctx.strokeStyle = '#e3e7ee'; ctx.lineWidth = 2 * dp; ctx.fillStyle = '#e3e7ee22'; ctx.beginPath();
  [[front, half], [front, -half], [back, -half], [back, half]].forEach((p, i) => { const q = W(p[0], p[1]); ctx[i ? 'lineTo' : 'moveTo'](q[0], q[1]); }); ctx.closePath(); ctx.fill(); ctx.stroke();
  const a0 = W(0, 0), a1 = W(front + 0.35, 0); ctx.beginPath(); ctx.moveTo(a0[0], a0[1]); ctx.lineTo(a1[0], a1[1]); ctx.stroke();
  v.lasers.forEach((l, i) => {
    const q = W(l.x, l.y), col = SCANC[i % SCANC.length]; ctx.fillStyle = col; ctx.beginPath(); ctx.arc(q[0], q[1], 5 * dp, 0, 7); ctx.fill();
    if (k > 25) { const t = l.name + (l.hasZ ? '  ' + l.z.toFixed(3) + ' m high' : ''); ctx.fillStyle = '#0e1014dd'; ctx.fillRect(q[0] + 8 * dp, q[1] - 9 * dp, ctx.measureText(t).width + 8 * dp, 17 * dp); ctx.fillStyle = col; ctx.fillText(t, q[0] + 12 * dp, q[1] + 4 * dp); }
  });
}

function scanInfo(v) {
  const box = $('#scanInfo'); box.replaceChildren();
  box.append(el('p', { textContent: (v.sample ? 'Sample ' + v.sample + ' of ' + v.count : 'LIVE ' + v.source) + ' · robot at ' + v.x.toFixed(2) + ', ' + v.y.toFixed(2) + ' · confidence ' + v.confidence.toFixed(2) + (v.station ? ' · ' + v.station : '') }));
  v.lasers.forEach((l, i) => box.append(el('div', { className: 'scanL' }, el('i', { style: 'background:' + SCANC[i % SCANC.length] }),
    el('span', {}, el('b', { textContent: l.name + ' (' + l.where + ')' }), ' ' + l.model, el('br'),
      el('b', { className: 'h', textContent: l.hasZ ? 'scan plane ' + l.z.toFixed(3) + ' m (' + Math.round(l.z * 1000) + ' mm) above the floor' : 'height not reported' }), el('br'),
      'sees ' + (l.maxAngle - l.minAngle) + '° up to ' + l.maxRange + ' m · ' + l.valid + ' of ' + l.beams + ' beams hit something, ' + l.nearest.toFixed(2) + ' to ' + l.farthest.toFixed(2) + ' m' + (l.forLoc ? ' · used for localisation: ' + l.forLoc : '')))));
}
function scanShow(v, follow) {
  S.ov.scan = { v, P: v.pts.map(f32) }; scanInfo(v);
  if (v.count) { $('#scanSl').max = v.count; $('#scanSl').value = v.sample; $('#scanI').value = v.sample; }
  if (!S.layers.lasers) { const c = $('#toolbar input[data-l=lasers]'); c.checked = true; S.layers.lasers = true; }
  const off = Math.abs(X(v.x) - cv.width / 2) > cv.width * 0.42 || Math.abs(Y(v.y) - cv.height / 2) > cv.height * 0.42;
  if (follow || off) { S.view.cx = v.x; S.view.cy = v.y; if (follow && S.view.k < 20) S.view.k = 28; }
  draw();
}
async function scanLoad(i, follow) {
  const drive = $('#selDrive').value; if (!drive) return out('LiDAR scan', 'Choose a recording first.');
  if (scanBusy) return; scanBusy = true;
  try { const v = await api('/api/scan?drive=' + encodeURIComponent(drive) + '&i=' + i); scanShow(v, follow); out('LiDAR scan', v.text); if (!S.A) toast('Pick a map to see the scan on it.'); }
  catch (e) { out('LiDAR scan', e.message); }
  scanBusy = false;
}
const scanStep = d => { scanStop(); scanLoad(Math.max(1, (+$('#scanI').value || 1) + d)); };
function scanStop() { clearTimeout(scanLiveT); scanLiveT = 0; $('#scanLive').textContent = 'Live from robot'; $('#scanLive').classList.remove('primary'); }
async function scanLiveTick(first) {
  const robot = $('#scanRobot').value;
  try { const v = await api('/api/scan?robot=' + encodeURIComponent(robot)); if (!scanLiveT && !first) return; scanShow(v, first); if (first) out('LiDAR scan', v.text); }
  catch (e) { out('LiDAR scan', e.message); return scanStop(); }
  if (scanLiveT || first) scanLiveT = setTimeout(scanLiveTick, 1000);
}
$('#scanGo').onclick = () => { scanStop(); scanLoad(+$('#scanI').value || 1, true); };
$('#scanPrev').onclick = () => scanStep(-1);
$('#scanNext').onclick = () => scanStep(1);
$('#scanSl').oninput = () => { scanStop(); $('#scanI').value = $('#scanSl').value; scanLoad(+$('#scanSl').value); };
$('#scanI').onchange = () => { scanStop(); scanLoad(+$('#scanI').value || 1); };
$('#scanLive').onclick = () => {
  if (scanLiveT) return scanStop();
  if (!$('#scanRobot').value) return out('LiDAR scan', 'Add a robot first (left panel).');
  $('#scanLive').textContent = 'Stop live'; $('#scanLive').classList.add('primary'); scanLiveT = 1; scanLiveTick(true);
};
$('#scanOff').onclick = () => { scanStop(); S.ov.scan = null; $('#scanInfo').replaceChildren(); draw(); };
window.addEventListener('keydown', e => { if (!S.ov.scan || !S.ov.scan.v.sample || /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName)) return; if (e.key === 'ArrowRight') scanStep(e.shiftKey ? 10 : 1); if (e.key === 'ArrowLeft') scanStep(e.shiftKey ? -10 : -1); });
