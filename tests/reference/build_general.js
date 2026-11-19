// Build candidate: live fleet map with the whole east loop moved as ONE rigid piece (rotation + shift).
// Nothing inside the east loop changes relative to anything else inside it. West side is untouched.
// usage: node build_v5.js live.smap sep16.smap T.json out.smap cutSouth cutNorth [mapName]
const fs = require('fs');
const [liveF, sepF, tF, outF, cS, cN, mapName, cSL] = process.argv.slice(2);
const live = JSON.parse(fs.readFileSync(liveF, 'utf8'));
const sep = JSON.parse(fs.readFileSync(sepF, 'utf8'));
const TT = JSON.parse(fs.readFileSync(tF, 'utf8'));
const CUT_S = +cS, CUT_N = +cN, YMIN = -14, YMAX = 42;
const c = Math.cos(TT.th), s = Math.sin(TT.th);
const T = (x, y) => [c * x - s * y + TT.tx, s * x + c * y + TT.ty];
const r3 = v => Math.round(v * 1000) / 1000;
const CUT_SL = cSL === undefined ? +cS : +cSL;
const cut = y => (y < 1 ? CUT_SL : y < 3 ? CUT_SL + (CUT_S - CUT_SL) * (y - 1) / 2 : y < 29 ? CUT_S : y < 31 ? CUT_S + (CUT_N - CUT_S) * (y - 29) / 2 : CUT_N);
const inE = (x, y) => y >= YMIN && y <= YMAX && x >= cut(y);
// old-map membership (Sep 16 walls, same coordinates) -> never moved
const C = 0.1, key = (x, y) => Math.round(x / C) * 100003 + Math.round(y / C);
const old = new Set();
for (const p of sep.normalPosList) { const cx = Math.round((p.x || 0) / C), cy = Math.round((p.y || 0) / C); for (let i = -1; i <= 1; i++) for (let j = -1; j <= 1; j++) old.add((cx + i) * 100003 + cy + j); }
const isOld = (x, y) => old.has(key(x, y));
const rep = {};
const setPos = (o, x, y) => { o.x = x; o.y = y; };

// ---- walls
let mv = 0, keptOldInE = 0;
const keptOld = new Set(); const movedWall = new Set(); // cells (5 cm) of ORIGINAL positions of moved walls, for feature-line decision
const k5 = (x, y) => Math.round(x / 0.05) * 200003 + Math.round(y / 0.05);
for (const p of live.normalPosList) {
  const x = p.x || 0, y = p.y || 0;
  if (!inE(x, y)) continue;
  if (isOld(x, y)) { keptOldInE++; keptOld.add(k5(x, y)); continue; }
  movedWall.add(k5(x, y));
  const [X, Y] = T(x, y); setPos(p, r3(X), r3(Y)); mv++;
}
rep.walls = { total: live.normalPosList.length, moved: mv, oldKeptInsideEastArea: keptOldInE };
// ---- rssi
mv = 0;
for (const p of live.rssiPosList || []) { const x = p.x || 0, y = p.y || 0; if (inE(x, y) && !isOld(x, y)) { const [X, Y] = T(x, y); setPos(p, r3(X), r3(Y)); mv++; } }
rep.rssi = { total: (live.rssiPosList || []).length, moved: mv };
// ---- reflectors
mv = 0; const refl = [];
for (const p of live.reflectorPosList || []) { const x = p.x || 0, y = p.y || 0; if (inE(x, y)) { const [X, Y] = T(x, y); refl.push([x, y, r3(X), r3(Y)]); setPos(p, r3(X), r3(Y)); mv++; } }
rep.reflectors = { total: (live.reflectorPosList || []).length, moved: mv, list: refl };
// ---- stations
const moved = {}; const stOld = {};
for (const p of live.advancedPointList) {
  const x = p.pos.x || 0, y = p.pos.y || 0; stOld[p.instanceName] = [x, y];
  if (!inE(x, y)) continue;
  const [X, Y] = T(x, y); setPos(p.pos, r3(X), r3(Y));
  if (typeof p.dir === 'number') p.dir = p.dir + TT.th;
  moved[p.instanceName] = [r3(X), r3(Y)];
}
rep.stations = { total: live.advancedPointList.length, moved: Object.keys(moved).length };
// ---- paths
let both = 0, cross = [], none = 0;
for (const q of live.advancedCurveList) {
  const a = q.startPos.instanceName, b = q.endPos.instanceName; const ma = moved[a], mb = moved[b];
  if (ma) setPos(q.startPos.pos, ma[0], ma[1]);
  if (mb) setPos(q.endPos.pos, mb[0], mb[1]);
  if (q.controlPos1 && ma) { const [X, Y] = T(q.controlPos1.x || 0, q.controlPos1.y || 0); setPos(q.controlPos1, r3(X), r3(Y)); }
  if (q.controlPos2 && mb) { const [X, Y] = T(q.controlPos2.x || 0, q.controlPos2.y || 0); setPos(q.controlPos2, r3(X), r3(Y)); }
  if (ma && mb) both++; else if (ma || mb) cross.push(q.instanceName); else none++;
}
rep.paths = { total: live.advancedCurveList.length, insideEast: both, crossingTheBoundary: cross, west: none };
// ---- feature lines
mv = 0; const b64 = v => Buffer.from(String(v)).toString('base64');
for (const l of live.advancedLineList) {
  const a = l.line.startPos, b = l.line.endPos; const ax = a.x || 0, ay = a.y || 0, bx = b.x || 0, by = b.y || 0;
  const mx = (ax + bx) / 2, my = (ay + by) / 2;
  if (!inE(mx, my)) continue;
  // stays only if it sits on an old (Sep 16) wall that stayed in place; otherwise it moves with the east loop
  let onOld = 0;
  for (const [x, y] of [[mx, my], [ax, ay], [bx, by]]) { const cx = Math.round(x / 0.05), cy = Math.round(y / 0.05); let h = false; for (let i = -2; i <= 2 && !h; i++) for (let j = -2; j <= 2; j++) if (keptOld.has((cx + i) * 200003 + cy + j)) { h = true; break; } if (h) onOld++; }
  if (onOld >= 2) continue;
  let [X, Y] = T(ax, ay); setPos(a, r3(X), r3(Y));[X, Y] = T(bx, by); setPos(b, r3(X), r3(Y));
  const px = (l.property || []).find(p => p.key === 'directionPosX'), py = (l.property || []).find(p => p.key === 'directionPosY');
  if (px && py) { const [DX, DY] = T(px.doubleValue || 0, py.doubleValue || 0); px.doubleValue = r3(DX); px.value = b64(r3(DX)); py.doubleValue = r3(DY); py.value = b64(r3(DY)); }
  mv++;
}
rep.featureLines = { total: live.advancedLineList.length, moved: mv };
// ---- areas
rep.areas = [];
for (const a of live.advancedAreaList || []) {
  const cx = a.posGroup.reduce((t, p) => t + (p.x || 0), 0) / a.posGroup.length, cy = a.posGroup.reduce((t, p) => t + (p.y || 0), 0) / a.posGroup.length;
  const m = inE(cx, cy);
  rep.areas.push({ name: a.className + ' ' + a.instanceName, centre: [r3(cx), r3(cy)], moved: m });
  if (m) { for (const p of a.posGroup) { const [X, Y] = T(p.x || 0, p.y || 0); setPos(p, r3(X), r3(Y)); } if (typeof a.dir === 'number') a.dir += TT.th; }
}
// ---- bins
mv = 0;
for (const g of live.binLocationsList || []) for (const bl of g.binLocationList || []) {
  if (!moved[bl.pointName]) continue;
  const [X, Y] = T(bl.pos.x || 0, bl.pos.y || 0); setPos(bl.pos, r3(X), r3(Y));
  for (const pr of bl.property || []) if (pr.key === 'points' && pr.stringValue) {
    const arr = JSON.parse(pr.stringValue); for (const q of arr) { const [A, B] = T(q.x, q.y); q.x = A; q.y = B; } pr.stringValue = JSON.stringify(arr);
  }
  mv++;
}
rep.bins = { moved: mv };
// ---- header bounds
let x0 = live.header.minPos.x, y0 = live.header.minPos.y, x1 = live.header.maxPos.x, y1 = live.header.maxPos.y;
for (const p of live.normalPosList) { const x = p.x || 0, y = p.y || 0; if (x < x0) x0 = x; if (y < y0) y0 = y; if (x > x1) x1 = x; if (y > y1) y1 = y; }
live.header.minPos.x = x0; live.header.minPos.y = y0; live.header.maxPos.x = x1; live.header.maxPos.y = y1;
if (mapName) live.header.mapName = mapName;
rep.header = live.header;
fs.writeFileSync(outF, JSON.stringify(live));
fs.writeFileSync(outF + '.moved.json', JSON.stringify({ T: TT, cutSouth: CUT_S, cutNorth: CUT_N, moved, stOld }));
rep.movedStationsNearBoundary = Object.keys(moved).filter(n => stOld[n][0] < cut(stOld[n][1]) + 6).map(n => n + ' ' + stOld[n].map(v => v.toFixed(1)).join(','));
rep.westStationsNearBoundary = Object.keys(stOld).filter(n => !moved[n] && stOld[n][1] >= YMIN && stOld[n][1] <= YMAX && stOld[n][0] > cut(stOld[n][1]) - 5).map(n => n + ' ' + stOld[n].map(v => v.toFixed(1)).join(','));
console.log(JSON.stringify(rep, null, 1).replace(/\n\s+(?=[-\d"\]])/g, ' '));
