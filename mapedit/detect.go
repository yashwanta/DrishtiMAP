package mapedit

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"drishtimap/smap"
)

// Finding is one thing the detector noticed on a map.
type Finding struct {
	Kind  string    `json:"kind"`
	Level string    `json:"level"` // fault | warn | info
	X     float64   `json:"x"`
	Y     float64   `json:"y"`
	HasXY bool      `json:"hasXY"`
	Near  string    `json:"near,omitempty"` // nearest station
	Text  string    `json:"text"`
	Fix   string    `json:"fix,omitempty"`  // what to do / which work instruction
	Line  []float64 `json:"line,omitempty"` // a feature line to highlight
}

// nearestStation names the closest station to a point.
func nearestStation(sts []smap.Station, p smap.Pt) string {
	best, name := 1e18, ""
	for _, s := range sts {
		if d := math.Hypot(s.Pos.X-p.X, s.Pos.Y-p.Y); d < best {
			best, name = d, s.Name
		}
	}
	return name
}

// densePath returns points along a path about every 5 cm.
func densePath(p smap.Path) []smap.Pt {
	s := p.Sample()
	var out []smap.Pt
	for i := 1; i < len(s); i++ {
		a, b := s[i-1], s[i]
		n := int(math.Ceil(math.Hypot(b.X-a.X, b.Y-a.Y) / 0.05))
		if n < 1 {
			n = 1
		}
		for k := 0; k < n; k++ {
			t := float64(k) / float64(n)
			out = append(out, smap.Pt{X: a.X + t*(b.X-a.X), Y: a.Y + t*(b.Y-a.Y)})
		}
	}
	return append(out, s[len(s)-1])
}

// Detect inspects one map file on its own (no recording) and lists what could trouble a robot.
func Detect(m *smap.Map) []Finding {
	var out []Finding
	sts := m.Stations()
	paths := m.Paths()
	at := func(kind, level string, p smap.Pt, text, fix string) *Finding {
		out = append(out, Finding{Kind: kind, Level: level, X: smap.R3(p.X), Y: smap.R3(p.Y), HasXY: true, Near: nearestStation(sts, p), Text: text, Fix: fix})
		return &out[len(out)-1]
	}
	// 1 structure
	for _, s := range Structure(m) {
		out = append(out, Finding{Kind: "structure", Level: "fault", Text: s, Fix: "Edit tab: move the station or delete and re-add the path (WI-06)"})
	}
	// 2 feature lines on or beside paths
	lines := m.Lines()
	type lh struct {
		d    float64
		path string
		p    smap.Pt
	}
	best := map[int]lh{}
	idx := map[cell][]int{}
	for i, l := range lines {
		x0, x1 := int(math.Floor(math.Min(l.A.X, l.B.X)-0.5)), int(math.Floor(math.Max(l.A.X, l.B.X)+0.5))
		y0, y1 := int(math.Floor(math.Min(l.A.Y, l.B.Y)-0.5)), int(math.Floor(math.Max(l.A.Y, l.B.Y)+0.5))
		for x := x0; x <= x1; x++ {
			for y := y0; y <= y1; y++ {
				idx[cell{x, y}] = append(idx[cell{x, y}], i)
			}
		}
	}
	dense := map[string][]smap.Pt{}
	for _, q := range paths {
		dense[q.Name] = densePath(q)
		for _, p := range dense[q.Name] {
			for _, i := range idx[cell{int(math.Floor(p.X)), int(math.Floor(p.Y))}] {
				d := distSeg(p, lines[i].A, lines[i].B)
				if b, ok := best[i]; d < 0.45 && (!ok || d < b.d) {
					best[i] = lh{d, q.Name, p}
				}
			}
		}
	}
	var li []int
	for i := range best {
		li = append(li, i)
	}
	sort.Slice(li, func(a, b int) bool {
		if best[li[a]].d != best[li[b]].d {
			return best[li[a]].d < best[li[b]].d
		}
		return li[a] < li[b]
	})
	for _, i := range li {
		b, l := best[i], lines[i]
		level, text := "warn", fmt.Sprintf("feature line #%d (%.2f,%.2f)-(%.2f,%.2f) is %.2f m from path %s", i, l.A.X, l.A.Y, l.B.X, l.B.Y, b.d, b.path)
		if b.d < 0.30 {
			level, text = "fault", fmt.Sprintf("feature line #%d (%.2f,%.2f)-(%.2f,%.2f) is %.2f m from the middle of path %s, inside the robot's body: a robot driving here raises a virtual-laser fault", i, l.A.X, l.A.Y, l.B.X, l.B.Y, b.d, b.path)
		}
		f := at("line_on_path", level, b.p, text, "Edit tab: Delete line, click it (WI-06)")
		f.Line = []float64{l.A.X, l.A.Y, l.B.X, l.B.Y}
	}
	// 3 wall dots on paths
	g := smap.NewNearGrid(m.Walls())
	cls := map[string]string{}
	for _, s := range sts {
		cls[s.Name] = s.Class
	}
	type ph struct {
		name string
		n    int
		min  float64
		p    smap.Pt
		dock bool
	}
	var phs []ph
	for _, q := range paths {
		h := ph{name: q.Name, min: 9, dock: cls[q.From] != "LocationMark" || cls[q.To] != "LocationMark"}
		for _, p := range dense[q.Name] {
			if d := g.Near(p); d < 0.15 {
				h.n++
				if d < h.min {
					h.min, h.p = d, p
				}
			}
		}
		if h.n >= 4 { // at least 0.2 m of the path runs through mapped walls
			phs = append(phs, h)
		}
	}
	sort.Slice(phs, func(i, j int) bool {
		if phs[i].dock != phs[j].dock {
			return !phs[i].dock
		}
		if phs[i].n != phs[j].n {
			return phs[i].n > phs[j].n
		}
		return phs[i].name < phs[j].name
	})
	for _, h := range phs {
		if h.dock {
			at("walls_on_path", "info", h.p, fmt.Sprintf("path %s runs %.1f m through mapped wall dots (it leads to a dock or parking position, where a mapped shelf or charger is normal)", h.name, float64(h.n)*0.05), "")
		} else {
			at("walls_on_path", "warn", h.p, fmt.Sprintf("path %s runs %.1f m through mapped wall dots: something that stood on the route was scanned into the map", h.name, float64(h.n)*0.05), "Edit tab: Erase walls, draw a box around the dots (WI-06)")
		}
	}
	// 4 stations without a path
	used := map[string]bool{}
	for _, q := range paths {
		used[q.From], used[q.To] = true, true
	}
	for _, s := range sts {
		if !used[s.Name] {
			at("lonely_station", "info", s.Pos, fmt.Sprintf("station %s has no path", s.Name), "")
		}
	}
	// 5 header
	if h := m.Root.O("header"); h != nil && h.O("minPos") != nil && h.O("maxPos") != nil {
		mn, mx := m.Bounds()
		if h.O("minPos").F("x") > mn.X || h.O("minPos").F("y") > mn.Y || h.O("maxPos").F("x") < mx.X || h.O("maxPos").F("y") < mx.Y {
			out = append(out, Finding{Kind: "header", Level: "fault", Text: "the header bounds do not cover all wall dots", Fix: "any Build or Save in this app corrects the header"})
		}
	}
	return out
}

// DetectText renders findings grouped by level, at most max lines per kind.
func DetectText(fs []Finding, max int) string {
	var b strings.Builder
	title := map[string]string{"fault": "FAULTS (fix before the map goes on a robot)", "warn": "WARNINGS (look at each one)", "info": "NOTES"}
	for _, lv := range []string{"fault", "warn", "info"} {
		byKind := map[string][]Finding{}
		var kinds []string
		for _, f := range fs {
			if f.Level == lv {
				if byKind[f.Kind] == nil {
					kinds = append(kinds, f.Kind)
				}
				byKind[f.Kind] = append(byKind[f.Kind], f)
			}
		}
		if len(kinds) == 0 {
			continue
		}
		b.WriteString(title[lv] + "\n")
		for _, k := range kinds {
			l := byKind[k]
			for i, f := range l {
				if i == max {
					fmt.Fprintf(&b, "  ... and %d more of this kind\n", len(l)-max)
					break
				}
				where := ""
				if f.HasXY {
					where = fmt.Sprintf("  [near %s, at %.1f, %.1f]", f.Near, f.X, f.Y)
				}
				fmt.Fprintf(&b, "  %s%s\n", f.Text, where)
			}
			if l[0].Fix != "" {
				b.WriteString("    -> " + l[0].Fix + "\n")
			}
		}
	}
	if b.Len() == 0 {
		return "nothing found\n"
	}
	return b.String()
}
