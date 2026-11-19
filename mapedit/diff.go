package mapedit

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"drishtimap/smap"
)

// Info is the summary of one map file.
type Info struct {
	File         string         `json:"file"`
	MapName      string         `json:"mapName"`
	MD5          string         `json:"md5"`
	SHA256       string         `json:"sha256"`
	SizeMB       float64        `json:"sizeMB"`
	Walls        int            `json:"walls"`
	Rssi         int            `json:"reflectionPoints"`
	Reflectors   int            `json:"reflectors"`
	Stations     int            `json:"stations"`
	Paths        int            `json:"paths"`
	Lines        int            `json:"featureLines"`
	Areas        int            `json:"areas"`
	Bins         int            `json:"bins"`
	MinX         float64        `json:"minX"`
	MinY         float64        `json:"minY"`
	MaxX         float64        `json:"maxX"`
	MaxY         float64        `json:"maxY"`
	Resolution   float64        `json:"resolution"`
	AreaTypes    map[string]int `json:"areaTypes"`
	StationTypes map[string]int `json:"stationTypes"`
}

// Describe summarises a map.
func Describe(m *smap.Map) Info {
	mn, mx := m.Bounds()
	i := Info{File: m.File, MapName: m.Name(), MD5: m.MD5, SHA256: m.SHA256, SizeMB: math.Round(float64(m.Size)/1e4) / 100,
		Walls: len(m.Walls()), Rssi: len(m.Root.A("rssiPosList")), Reflectors: len(m.Root.A("reflectorPosList")),
		Stations: len(m.Root.A("advancedPointList")), Paths: len(m.Root.A("advancedCurveList")), Lines: len(m.Root.A("advancedLineList")),
		Areas: len(m.Root.A("advancedAreaList")), MinX: mn.X, MinY: mn.Y, MaxX: mx.X, MaxY: mx.Y,
		Resolution: m.Root.O("header").F("resolution"), AreaTypes: map[string]int{}, StationTypes: map[string]int{}}
	for _, g := range m.List("binLocationsList") {
		i.Bins += len(g.A("binLocationList"))
	}
	for _, a := range m.List("advancedAreaList") {
		i.AreaTypes[a.S("className")]++
	}
	for _, s := range m.Stations() {
		i.StationTypes[s.Class]++
	}
	return i
}

// Diff is what changed from map A to map B.
type Diff struct {
	StationsAdded, StationsRemoved []string
	StationsMoved                  []string // "LM443 0.52 m"
	StationsChanged                []string // direction / property only
	PathsAdded, PathsRemoved       []string
	PathsChanged                   []string
	WallsOnlyA, WallsOnlyB         int
	LinesOnlyA, LinesOnlyB         int
	RssiA, RssiB                   int
	ReflA, ReflB                   int
	AreasA, AreasB                 int
	AreasChanged                   []string
	Header                         []string
	WallBoxesB                     []string    // where the new walls are
	WallBoxesA                     []string    // where walls were removed
	LinesB                         []smap.Line `json:"-"`
	LinesA                         []smap.Line `json:"-"`
}

func wallCells(pts []smap.Pt) map[cell]int {
	g := make(map[cell]int, len(pts))
	for _, p := range pts {
		g[cellOf(p.X, p.Y, 0.001)]++
	}
	return g
}

// onlyIn counts dots of a that b does not have at the same position (to the millimetre), and where they are (10 m boxes).
func onlyIn(a []smap.Pt, b map[cell]int) (int, []string) {
	n := 0
	box := map[cell]int{}
	b2 := make(map[cell]int, len(b))
	for k, v := range b {
		b2[k] = v
	}
	for _, p := range a {
		c := cellOf(p.X, p.Y, 0.001)
		hit := b2[c] > 0
		if hit {
			b2[c]--
		}
		if !hit {
			n++
			box[cell{int(math.Floor(p.X / 10)), int(math.Floor(p.Y / 10))}]++
		}
	}
	type kv struct {
		c cell
		n int
	}
	var l []kv
	for c, k := range box {
		l = append(l, kv{c, k})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].n != l[j].n {
			return l[i].n > l[j].n
		}
		return l[i].c[0]*1000+l[i].c[1] < l[j].c[0]*1000+l[j].c[1]
	})
	var out []string
	for i, e := range l {
		if i == 8 {
			break
		}
		out = append(out, fmt.Sprintf("x %d..%d y %d..%d: %d dots", e.c[0]*10, e.c[0]*10+10, e.c[1]*10, e.c[1]*10+10, e.n))
	}
	return n, out
}

// Compare lists the differences between two maps.
func Compare(a, b *smap.Map) *Diff {
	d := &Diff{}
	as, bs := map[string]smap.Station{}, map[string]smap.Station{}
	for _, s := range a.Stations() {
		as[s.Name] = s
	}
	for _, s := range b.Stations() {
		bs[s.Name] = s
		o, ok := as[s.Name]
		switch {
		case !ok:
			d.StationsAdded = append(d.StationsAdded, s.Name)
		case o.Pos != s.Pos:
			d.StationsMoved = append(d.StationsMoved, fmt.Sprintf("%s %.2f m (%.2f,%.2f) -> (%.2f,%.2f)", s.Name, math.Hypot(o.Pos.X-s.Pos.X, o.Pos.Y-s.Pos.Y), o.Pos.X, o.Pos.Y, s.Pos.X, s.Pos.Y))
		case !same(o.O, s.O):
			t := propDiff(o.O, s.O)
			if math.Abs(o.Dir-s.Dir) > 1e-6 {
				t = fmt.Sprintf("direction %.1f -> %.1f deg", o.Dir*180/math.Pi, s.Dir*180/math.Pi)
			}
			d.StationsChanged = append(d.StationsChanged, s.Name+": "+t)
		}
	}
	for _, s := range a.Stations() {
		if _, ok := bs[s.Name]; !ok {
			d.StationsRemoved = append(d.StationsRemoved, s.Name)
		}
	}
	ap, bp := map[string]smap.Path{}, map[string]smap.Path{}
	for _, p := range a.Paths() {
		ap[p.Name] = p
	}
	for _, p := range b.Paths() {
		bp[p.Name] = p
		o, ok := ap[p.Name]
		if !ok {
			d.PathsAdded = append(d.PathsAdded, p.Name)
			continue
		}
		if same(o.O, p.O) {
			continue
		}
		var t []string
		if !same(o.O.Get("property"), p.O.Get("property")) || o.Class != p.Class {
			t = append(t, propDiff(o.O, p.O))
		}
		la, lb := smap.Len(o.Sample()), smap.Len(p.Sample())
		if o.P0 != p.P0 || o.P3 != p.P3 || !ptEq(o.C1, p.C1) || !ptEq(o.C2, p.C2) {
			t = append(t, fmt.Sprintf("shape changed, length %.2f -> %.2f m", la, lb))
		}
		if len(t) == 0 {
			t = append(t, "other fields changed")
		}
		d.PathsChanged = append(d.PathsChanged, p.Name+": "+strings.Join(t, "; "))
	}
	for _, p := range a.Paths() {
		if _, ok := bp[p.Name]; !ok {
			d.PathsRemoved = append(d.PathsRemoved, p.Name)
		}
	}
	d.WallsOnlyA, d.WallBoxesA = onlyIn(a.Walls(), wallCells(b.Walls()))
	d.WallsOnlyB, d.WallBoxesB = onlyIn(b.Walls(), wallCells(a.Walls()))
	lk := func(l smap.Line) string {
		return fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", l.A.X, l.A.Y, l.B.X, l.B.Y)
	}
	la, lb := map[string]int{}, map[string]int{}
	for _, l := range a.Lines() {
		la[lk(l)]++
	}
	for _, l := range b.Lines() {
		lb[lk(l)]++
		if la[lk(l)] == 0 {
			d.LinesOnlyB++
			d.LinesB = append(d.LinesB, l)
		}
	}
	for _, l := range a.Lines() {
		if lb[lk(l)] == 0 {
			d.LinesOnlyA++
			d.LinesA = append(d.LinesA, l)
		}
	}
	d.RssiA, d.RssiB = len(a.Root.A("rssiPosList")), len(b.Root.A("rssiPosList"))
	d.ReflA, d.ReflB = len(a.Root.A("reflectorPosList")), len(b.Root.A("reflectorPosList"))
	aa, ba := a.List("advancedAreaList"), b.List("advancedAreaList")
	d.AreasA, d.AreasB = len(aa), len(ba)
	am := map[string]*smap.Obj{}
	for _, o := range aa {
		am[o.S("className")+" "+o.S("instanceName")] = o
	}
	for _, o := range ba {
		k := o.S("className") + " " + o.S("instanceName")
		if p, ok := am[k]; !ok {
			d.AreasChanged = append(d.AreasChanged, k+": added")
		} else if !same(p, o) {
			d.AreasChanged = append(d.AreasChanged, k+": changed")
		}
		delete(am, k)
	}
	for k := range am {
		d.AreasChanged = append(d.AreasChanged, k+": removed")
	}
	sort.Strings(d.AreasChanged)
	ha, hb := a.Root.O("header"), b.Root.O("header")
	if ha.S("mapName") != hb.S("mapName") {
		d.Header = append(d.Header, "map name "+ha.S("mapName")+" -> "+hb.S("mapName"))
	}
	for _, k := range []string{"minPos", "maxPos"} {
		if ha.O(k) != nil && hb.O(k) != nil && smap.P(ha.O(k)) != smap.P(hb.O(k)) {
			d.Header = append(d.Header, fmt.Sprintf("%s (%v,%v) -> (%v,%v)", k, ha.O(k).F("x"), ha.O(k).F("y"), hb.O(k).F("x"), hb.O(k).F("y")))
		}
	}
	if ha.F("resolution") != hb.F("resolution") {
		d.Header = append(d.Header, fmt.Sprintf("resolution %v -> %v", ha.F("resolution"), hb.F("resolution")))
	}
	return d
}

func ptEq(a, b *smap.Pt) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Text renders the difference as plain lines. nameA/nameB label the two maps.
func (d *Diff) Text(nameA, nameB string) string {
	var b strings.Builder
	list := func(title string, l []string, max int) {
		if len(l) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s: %d\n", title, len(l))
		for _, s := range clip(l, max) {
			b.WriteString("  " + s + "\n")
		}
	}
	fmt.Fprintf(&b, "A = %s, B = %s\n", nameA, nameB)
	list("stations only in B", d.StationsAdded, 30)
	list("stations only in A", d.StationsRemoved, 30)
	list("stations at a different position", d.StationsMoved, 60)
	list("stations with other changes", d.StationsChanged, 30)
	list("paths only in B", d.PathsAdded, 30)
	list("paths only in A", d.PathsRemoved, 30)
	list("paths changed", d.PathsChanged, 60)
	fmt.Fprintf(&b, "wall dots only in A: %d, only in B: %d\n", d.WallsOnlyA, d.WallsOnlyB)
	for _, s := range d.WallBoxesA {
		b.WriteString("  only A  " + s + "\n")
	}
	for _, s := range d.WallBoxesB {
		b.WriteString("  only B  " + s + "\n")
	}
	fmt.Fprintf(&b, "feature lines only in A: %d, only in B: %d\n", d.LinesOnlyA, d.LinesOnlyB)
	if d.RssiA != d.RssiB {
		fmt.Fprintf(&b, "reflection points: %d -> %d\n", d.RssiA, d.RssiB)
	}
	if d.ReflA != d.ReflB {
		fmt.Fprintf(&b, "reflectors: %d -> %d\n", d.ReflA, d.ReflB)
	}
	list("areas changed", d.AreasChanged, 30)
	list("header", d.Header, 10)
	if len(d.StationsAdded)+len(d.StationsRemoved)+len(d.StationsMoved)+len(d.StationsChanged)+len(d.PathsAdded)+len(d.PathsRemoved)+len(d.PathsChanged)+
		d.WallsOnlyA+d.WallsOnlyB+d.LinesOnlyA+d.LinesOnlyB+len(d.AreasChanged)+len(d.Header) == 0 && d.RssiA == d.RssiB && d.ReflA == d.ReflB {
		b.WriteString("no differences found\n")
	}
	return b.String()
}
