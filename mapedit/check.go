package mapedit

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"

	"drishtimap/smap"
)

// Check is one pass/fail line of the candidate report.
type Check struct {
	OK      bool     `json:"ok"`
	Text    string   `json:"text"`
	Details []string `json:"details,omitempty"`
}

// NearLine is a feature line that sits close to a path.
type NearLine struct {
	Path    string  `json:"path"`
	Base    float64 `json:"baseDist"`
	Cand    float64 `json:"candDist"`
	LineIdx int     `json:"lineIndex"`
	A       smap.Pt `json:"a"`
	B       smap.Pt `json:"b"`
}

// VerifyResult is the full check report for a candidate against the map it was built from.
type VerifyResult struct {
	Checks    []Check    `json:"checks"`
	AllOK     bool       `json:"allOk"`
	NearLines []NearLine `json:"nearLines"`
}

// Text renders the report as plain lines.
func (r *VerifyResult) Text() string {
	var b strings.Builder
	for _, c := range r.Checks {
		if c.OK {
			b.WriteString("PASS  ")
		} else {
			b.WriteString("FAIL  ")
		}
		b.WriteString(c.Text + "\n")
		for _, d := range c.Details {
			b.WriteString("        " + d + "\n")
		}
	}
	if r.AllOK {
		b.WriteString("RESULT: ALL CHECKS PASS\n")
	} else {
		n := 0
		for _, c := range r.Checks {
			if !c.OK {
				n++
			}
		}
		fmt.Fprintf(&b, "RESULT: %d CHECK(S) FAILED\n", n)
	}
	return b.String()
}

func distSeg(p, a, b smap.Pt) float64 {
	vx, vy := b.X-a.X, b.Y-a.Y
	l2 := vx*vx + vy*vy
	t := 0.0
	if l2 > 0 {
		t = ((p.X-a.X)*vx + (p.Y-a.Y)*vy) / l2
	}
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.X-a.X-t*vx, p.Y-a.Y-t*vy)
}

type lineHit struct {
	d float64
	l *smap.Line
}

// lineClearance: for each path, the nearest feature line.
func lineClearance(m *smap.Map) map[string]lineHit {
	lines := m.Lines()
	idx := map[cell][]*smap.Line{}
	for i := range lines {
		l := &lines[i]
		x0, x1 := int(math.Floor(math.Min(l.A.X, l.B.X)/2)), int(math.Floor(math.Max(l.A.X, l.B.X)/2))
		y0, y1 := int(math.Floor(math.Min(l.A.Y, l.B.Y)/2)), int(math.Floor(math.Max(l.A.Y, l.B.Y)/2))
		for x := x0; x <= x1; x++ {
			for y := y0; y <= y1; y++ {
				idx[cell{x, y}] = append(idx[cell{x, y}], l)
			}
		}
	}
	res := map[string]lineHit{}
	for _, q := range m.Paths() {
		best := lineHit{d: 9}
		for _, p := range q.Sample() {
			cx, cy := int(math.Floor(p.X/2)), int(math.Floor(p.Y/2))
			for i := -1; i <= 1; i++ {
				for j := -1; j <= 1; j++ {
					for _, l := range idx[cell{cx + i, cy + j}] {
						if d := distSeg(p, l.A, l.B); d < best.d {
							best = lineHit{d, l}
						}
					}
				}
			}
		}
		res[q.Name] = best
	}
	return res
}

// LinesNearPaths lists paths where a feature line is closer than limit in cand and closer than in base.
// A feature line across a path is what raised the "virtual laser" fault at LM514-LM515.
func LinesNearPaths(base, cand *smap.Map, limit float64) []NearLine {
	rl, rv := lineClearance(base), lineClearance(cand)
	var bad []NearLine
	for n, v := range rv {
		a := 9.0
		if b, ok := rl[n]; ok {
			a = b.d
		}
		if v.d < limit && v.d < a-0.03 {
			bad = append(bad, NearLine{Path: n, Base: a, Cand: v.d, LineIdx: v.l.Idx, A: v.l.A, B: v.l.B})
		}
	}
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].Cand != bad[j].Cand {
			return bad[i].Cand < bad[j].Cand
		}
		return bad[i].Path < bad[j].Path
	})
	return bad
}

func same(a, b any) bool { return smap.Equal(a, b) }

// Verify compares a candidate with its base map and reports everything that could hurt a robot:
// lost stations or paths, changed properties, paths or stations closer to walls, feature lines on paths.
func Verify(base, cand *smap.Map) *VerifyResult { return VerifyWith(base, cand, nil) }

// VerifyWith is Verify for a candidate with hand edits: stations and paths named in byHand were
// changed on purpose, so they are listed instead of failing the "identical to the base" checks.
// The clearance, feature-line and structure checks still apply to them.
func VerifyWith(base, cand *smap.Map, byHand map[string]bool) *VerifyResult {
	r := &VerifyResult{}
	add := func(ok bool, text string, details ...string) {
		r.Checks = append(r.Checks, Check{OK: ok, Text: text, Details: details})
	}
	bs, cs := base.Stations(), cand.Stations()
	bp, cp := base.Paths(), cand.Paths()
	bSt, cSt := map[string]smap.Station{}, map[string]smap.Station{}
	for _, s := range bs {
		bSt[s.Name] = s
	}
	for _, s := range cs {
		cSt[s.Name] = s
	}
	bPa, cPa := map[string]smap.Path{}, map[string]smap.Path{}
	for _, p := range bp {
		bPa[p.Name] = p
	}
	for _, p := range cp {
		cPa[p.Name] = p
	}
	// 1 nothing lost or added
	var lost []string
	for _, s := range bs {
		if _, ok := cSt[s.Name]; !ok && !byHand[s.Name] {
			lost = append(lost, "station missing: "+s.Name)
		}
	}
	for _, s := range cs {
		if _, ok := bSt[s.Name]; !ok && !byHand[s.Name] {
			lost = append(lost, "station added: "+s.Name)
		}
	}
	for _, p := range bp {
		if _, ok := cPa[p.Name]; !ok && !byHand[p.Name] {
			lost = append(lost, "path missing: "+p.Name)
		}
	}
	for _, p := range cp {
		if _, ok := bPa[p.Name]; !ok && !byHand[p.Name] {
			lost = append(lost, "path added: "+p.Name)
		}
	}
	nb := func(m *smap.Map, k string) int { return len(m.Root.A(k)) }
	sameCounts := len(lost) == 0 && nb(base, "binLocationsList") == nb(cand, "binLocationsList") &&
		nb(base, "advancedAreaList") == nb(cand, "advancedAreaList") && nb(base, "reflectorPosList") == nb(cand, "reflectorPosList")
	if len(lost) > 12 {
		lost = append(lost[:12], fmt.Sprintf("... and %d more", len(lost)-12))
	}
	what := "same stations"
	if len(byHand) > 0 {
		what = "apart from the hand edits listed below, same stations"
	}
	add(sameCounts, fmt.Sprintf(what+" (%d), paths (%d), bin groups (%d), areas (%d), reflectors (%d) as the base map",
		len(cs), len(cp), nb(cand, "binLocationsList"), nb(cand, "advancedAreaList"), nb(cand, "reflectorPosList")), lost...)

	// moved = station whose position differs
	moved := map[string]bool{}
	for _, s := range cs {
		if b, ok := bSt[s.Name]; ok && (b.Pos != s.Pos) && !byHand[s.Name] {
			moved[s.Name] = true
		}
	}
	// 2 unmoved stations identical
	var d2 []string
	for _, s := range cs {
		if b, ok := bSt[s.Name]; ok && !moved[s.Name] && !byHand[s.Name] && !same(b.O, s.O) {
			d2 = append(d2, s.Name+": direction or properties changed")
		}
	}
	add(len(d2) == 0, fmt.Sprintf("all %d stations that did not move are identical to the base map (direction, properties)", len(cs)-len(moved)), clip(d2, 12)...)
	// 3 paths: unmoved identical; all keep type and properties
	var d3 []string
	for _, p := range cp {
		b, ok := bPa[p.Name]
		if !ok || byHand[p.Name] {
			continue
		}
		if !same(b.O.Get("property"), p.O.Get("property")) || b.Class != p.Class {
			d3 = append(d3, p.Name+": properties changed ("+propDiff(b.O, p.O)+")")
		} else if !moved[p.From] && !moved[p.To] && !same(b.O, p.O) {
			d3 = append(d3, p.Name+": shape changed although neither end moved")
		}
	}
	add(len(d3) == 0, "paths that did not move are identical; every path keeps its type, direction, speed and other properties", clip(d3, 12)...)
	// 4 moved stations keep their spacing (one rigid piece)
	if len(moved) >= 2 {
		var names []string
		for n := range moved {
			names = append(names, n)
		}
		sort.Strings(names)
		maxd := 0.0
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				a, b, c, d := bSt[names[i]].Pos, bSt[names[j]].Pos, cSt[names[i]].Pos, cSt[names[j]].Pos
				if e := math.Abs(math.Hypot(a.X-b.X, a.Y-b.Y) - math.Hypot(c.X-d.X, c.Y-d.Y)); e > maxd {
					maxd = e
				}
			}
		}
		add(maxd < 0.003, fmt.Sprintf("the %d moved stations keep their exact spacing to each other (largest change %.1f mm)", len(names), maxd*1000))
	} else {
		add(true, fmt.Sprintf("stations moved: %d", len(moved)))
	}
	// 5 path length and wall clearance
	gB, gC := smap.NewNearGrid(base.Walls()), smap.NewNearGrid(cand.Walls())
	clear := func(g smap.NearGrid, pts []smap.Pt) float64 {
		c := 9.0
		for _, p := range pts {
			if d := g.Near(p); d < c {
				c = d
			}
		}
		return c
	}
	worstLen, minV, minName := 0.0, 9.0, ""
	var worse, cross []string
	for _, p := range cp {
		b, ok := bPa[p.Name]
		if !ok {
			continue
		}
		sa, sb := b.Sample(), p.Sample()
		la, lb := smap.Len(sa), smap.Len(sb)
		ca, cb := clear(gB, sa), clear(gC, sb)
		ma, mb := moved[p.From], moved[p.To]
		if ma && mb && !byHand[p.Name] {
			worstLen = math.Max(worstLen, math.Abs(la-lb))
		} else if ma || mb {
			cross = append(cross, fmt.Sprintf("%s: length %.2f -> %.2f m, clearance %.2f -> %.2f m", p.Name, la, lb, ca, cb))
		}
		if cb < minV {
			minV, minName = cb, p.Name
		}
		if cb < ca-0.05 && cb < 0.60 {
			worse = append(worse, fmt.Sprintf("%s: wall clearance base %.2f m -> candidate %.2f m", p.Name, ca, cb))
		}
	}
	add(worstLen < 0.005, fmt.Sprintf("paths between moved stations keep their length (largest change %.1f mm)", worstLen*1000))
	add(len(worse) == 0, fmt.Sprintf("no path is closer to a wall than on the base map (paths checked %d; tightest %.2f m at %s)", len(cp), minV, minName), clip(worse, 20)...)
	if len(cross) > 0 {
		r.Checks[len(r.Checks)-1].Details = append(r.Checks[len(r.Checks)-1].Details, append([]string{"paths crossing the boundary of the moved piece:"}, clip(cross, 20)...)...)
	}
	// 6 station clearance
	var sw []string
	for _, s := range cs {
		b, ok := bSt[s.Name]
		if !ok {
			continue
		}
		a, c := gB.Near(b.Pos), gC.Near(s.Pos)
		if c < a-0.05 && c < 0.6 {
			sw = append(sw, fmt.Sprintf("%s %.2f -> %.2f m", s.Name, a, c))
		}
	}
	add(len(sw) == 0, "no station is closer to a wall than on the base map", clip(sw, 20)...)
	// 7 bins still on their stations
	binsOff := func(m *smap.Map, st map[string]smap.Station) (off, n int) {
		for _, g := range m.List("binLocationsList") {
			for _, bl := range smap.Objs(g.A("binLocationList")) {
				n++
				if s, ok := st[bl.S("pointName")]; ok && bl.O("pos") != nil {
					q := smap.P(bl.O("pos"))
					if math.Hypot(q.X-s.Pos.X, q.Y-s.Pos.Y) > 0.002 {
						off++
					}
				}
			}
		}
		return
	}
	ob, _ := binsOff(base, bSt)
	oc, nc := binsOff(cand, cSt)
	add(oc == ob, fmt.Sprintf("bins sit on their stations exactly as on the base map (%d bins)", nc))
	// 8 header
	h, hb := cand.Root.O("header"), base.Root.O("header")
	mn, mx := cand.Bounds()
	okH := h != nil && h.O("minPos") != nil && h.O("maxPos") != nil && h.O("minPos").F("x") <= mn.X && h.O("minPos").F("y") <= mn.Y &&
		h.O("maxPos").F("x") >= mx.X && h.O("maxPos").F("y") >= mx.Y && h.F("resolution") == hb.F("resolution")
	if h != nil && h.O("minPos") != nil && h.O("maxPos") != nil {
		add(okH, fmt.Sprintf("header covers all walls (x %v..%v, y %v..%v), resolution unchanged", h.O("minPos").F("x"), h.O("maxPos").F("x"), h.O("minPos").F("y"), h.O("maxPos").F("y")))
	} else {
		add(false, "header has no bounds")
	}
	// 9 numbers
	bad := 0
	for _, w := range cand.Walls() {
		if math.IsNaN(w.X) || math.IsNaN(w.Y) || math.IsInf(w.X, 0) || math.IsInf(w.Y, 0) {
			bad++
		}
	}
	for _, s := range cs {
		if s.O.O("pos") == nil || math.IsNaN(s.Pos.X+s.Pos.Y+s.Dir) {
			bad++
		}
	}
	add(bad == 0, "no invalid numbers")
	// 10 feature lines on paths
	r.NearLines = LinesNearPaths(base, cand, 0.45)
	var d10 []string
	for _, n := range r.NearLines {
		d10 = append(d10, fmt.Sprintf("%-14s base %.2f m -> candidate %.2f m   line #%d (%.2f,%.2f)-(%.2f,%.2f)", n.Path, n.Base, n.Cand, n.LineIdx, n.A.X, n.A.Y, n.B.X, n.B.Y))
	}
	add(len(d10) == 0, "no feature line is closer than 0.45 m to a path unless it already was on the base map (a line on a path raises a virtual-laser fault)", clip(d10, 30)...)
	// 11 structure: nothing dangling that was not already so on the base map
	was := map[string]bool{}
	for _, x := range Structure(base) {
		was[x] = true
	}
	var d11 []string
	for _, x := range Structure(cand) {
		if !was[x] {
			d11 = append(d11, x)
		}
	}
	add(len(d11) == 0, "every path starts and ends on an existing station, every bin has its station, no name is used twice", clip(d11, 20)...)
	if len(byHand) > 0 {
		add(true, fmt.Sprintf("changed by hand, so not compared with the base map (%d): check each one is what you meant", len(byHand)), strings.Join(clip(sortedKeys(byHand), 40), " "))
	}

	r.AllOK = true
	for _, c := range r.Checks {
		if !c.OK {
			r.AllOK = false
		}
	}
	return r
}

func clip(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string{}, s[:n]...), fmt.Sprintf("... and %d more", len(s)-n))
	}
	return s
}

// propVal reads the typed value of a property entry as text.
func propVal(p *smap.Obj) string {
	for _, k := range []string{"stringValue", "boolValue", "int32Value", "uint32Value", "int64Value", "uint64Value", "floatValue", "doubleValue"} {
		if p.Has(k) {
			return string(bytes.Trim(smap.Marshal(p.Get(k)), `"`))
		}
	}
	return "0"
}

func props(o *smap.Obj) map[string]string {
	out := map[string]string{}
	for _, p := range smap.Objs(o.A("property")) {
		out[p.S("key")] = propVal(p)
	}
	return out
}

// propDiff describes which properties differ between two objects, e.g. "direction 1 -> 0".
func propDiff(a, b *smap.Obj) string {
	pa, pb := props(a), props(b)
	keys := map[string]bool{}
	for k := range pa {
		keys[k] = true
	}
	for k := range pb {
		keys[k] = true
	}
	var ks, out []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		va, oka := pa[k]
		vb, okb := pb[k]
		switch {
		case !oka:
			out = append(out, k+" added = "+vb)
		case !okb:
			out = append(out, k+" removed (was "+va+")")
		case va != vb:
			out = append(out, k+" "+va+" -> "+vb)
		}
	}
	if a.S("className") != b.S("className") {
		out = append(out, "type "+a.S("className")+" -> "+b.S("className"))
	}
	if len(out) == 0 {
		return "property encoding differs"
	}
	return strings.Join(out, ", ")
}

// PropText lists an object's properties as "key=value, key=value" (for display).
func PropText(o *smap.Obj) string {
	var out []string
	for _, p := range smap.Objs(o.A("property")) {
		out = append(out, p.S("key")+"="+propVal(p))
	}
	return strings.Join(out, ", ")
}
