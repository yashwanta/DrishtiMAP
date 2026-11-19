// Package mapedit builds candidate map files offline and checks them.
// Nothing here talks to a robot: a candidate is a file on disk that a person loads in RoboShop.
package mapedit

import (
	"encoding/base64"
	"fmt"
	"math"
	"sort"

	"drishtimap/smap"
)

// MoveParams describes "move one region of the map as a single rigid piece".
type MoveParams struct {
	T       smap.Rigid // rotation + shift applied to everything in the region
	Region  []smap.Pt  // polygon in the base map's coordinates
	Anchor  *smap.Map  // optional older map: walls that also exist there (within 0.1 m) stay where they are
	MapName string     // new header.mapName ("" = keep)
	Lines   string     // "auto": feature lines on anchored walls stay; "all": every line in the region moves
}

// MoveReport lists what was moved.
type MoveReport struct {
	RotationDeg     float64               `json:"rotationDeg"`
	Tx              float64               `json:"tx"`
	Ty              float64               `json:"ty"`
	Pivot           *smap.Pt              `json:"pivot,omitempty"`
	WallsTotal      int                   `json:"wallsTotal"`
	WallsMoved      int                   `json:"wallsMoved"`
	WallsAnchored   int                   `json:"wallsAnchoredInRegion"`
	RssiMoved       int                   `json:"rssiMoved"`
	ReflectorsMoved int                   `json:"reflectorsMoved"`
	StationsTotal   int                   `json:"stationsTotal"`
	StationsMoved   int                   `json:"stationsMoved"`
	Moved           map[string][2]float64 `json:"moved"`  // station -> new position
	Old             map[string][2]float64 `json:"oldPos"` // station -> position before (moved stations only)
	PathsInside     int                   `json:"pathsInside"`
	PathsCrossing   []string              `json:"pathsCrossingBoundary"`
	LinesTotal      int                   `json:"featureLinesTotal"`
	LinesMoved      int                   `json:"featureLinesMoved"`
	LinesKept       int                   `json:"featureLinesKeptOnAnchoredWalls"`
	AreasMoved      []string              `json:"areasMoved"`
	BinsMoved       int                   `json:"binsMoved"`
	LargestMove     float64               `json:"largestStationMove"`
	LargestMoveAt   string                `json:"largestStationMoveAt"`
}

type cell [2]int

func cellOf(x, y, c float64) cell { return cell{int(smap.JSRound(x / c)), int(smap.JSRound(y / c))} }

// MoveRegion edits m in place. The caller passes a clone of the base map.
func MoveRegion(m *smap.Map, p MoveParams) (*MoveReport, error) {
	if len(p.Region) < 3 {
		return nil, fmt.Errorf("the region needs at least 3 corner points")
	}
	T := p.T
	in := func(q smap.Pt) bool { return smap.InPoly(q, p.Region) }
	rep := &MoveReport{RotationDeg: T.Th * 180 / math.Pi, Tx: T.Tx, Ty: T.Ty, Moved: map[string][2]float64{}, Old: map[string][2]float64{}}
	if pv, ok := T.Pivot(); ok {
		rep.Pivot = &smap.Pt{X: smap.R3(pv.X), Y: smap.R3(pv.Y)}
	}
	setR := func(o *smap.Obj, q smap.Pt) { o.SetF("x", smap.R3(q.X)); o.SetF("y", smap.R3(q.Y)) }

	// walls of the anchor map (and their 8 neighbours on a 0.1 m grid) never move
	old := map[cell]struct{}{}
	if p.Anchor != nil {
		for _, w := range p.Anchor.Walls() {
			c := cellOf(w.X, w.Y, 0.1)
			for i := -1; i <= 1; i++ {
				for j := -1; j <= 1; j++ {
					old[cell{c[0] + i, c[1] + j}] = struct{}{}
				}
			}
		}
	}
	isOld := func(q smap.Pt) bool { _, ok := old[cellOf(q.X, q.Y, 0.1)]; return ok }

	// ---- walls
	keptOld := map[cell]struct{}{} // 5 cm cells of anchored walls inside the region, for the feature-line rule
	walls := m.List("normalPosList")
	rep.WallsTotal = len(walls)
	for _, o := range walls {
		q := smap.P(o)
		if !in(q) {
			continue
		}
		if isOld(q) {
			rep.WallsAnchored++
			keptOld[cellOf(q.X, q.Y, 0.05)] = struct{}{}
			continue
		}
		setR(o, T.Apply(q))
		rep.WallsMoved++
	}
	// ---- reflection points
	for _, o := range m.List("rssiPosList") {
		if q := smap.P(o); in(q) && !isOld(q) {
			setR(o, T.Apply(q))
			rep.RssiMoved++
		}
	}
	// ---- reflectors
	for _, o := range m.List("reflectorPosList") {
		if q := smap.P(o); in(q) {
			setR(o, T.Apply(q))
			rep.ReflectorsMoved++
		}
	}
	// ---- stations
	sts := m.List("advancedPointList")
	rep.StationsTotal = len(sts)
	for _, o := range sts {
		pos := o.O("pos")
		if pos == nil {
			continue
		}
		q := smap.P(pos)
		if !in(q) {
			continue
		}
		n := T.Apply(q)
		setR(pos, n)
		if o.IsNum("dir") {
			o.SetF("dir", o.F("dir")+T.Th)
		}
		name := o.S("instanceName")
		rep.Moved[name] = [2]float64{smap.R3(n.X), smap.R3(n.Y)}
		rep.Old[name] = [2]float64{q.X, q.Y}
		if d := math.Hypot(n.X-q.X, n.Y-q.Y); d > rep.LargestMove {
			rep.LargestMove, rep.LargestMoveAt = smap.R3(d), name
		}
	}
	rep.StationsMoved = len(rep.Moved)
	// ---- paths: each end follows its station, each control point follows its end
	for _, o := range m.List("advancedCurveList") {
		s, e := o.O("startPos"), o.O("endPos")
		ma, okA := rep.Moved[s.S("instanceName")]
		mb, okB := rep.Moved[e.S("instanceName")]
		if okA && s.O("pos") != nil {
			s.O("pos").SetF("x", ma[0])
			s.O("pos").SetF("y", ma[1])
		}
		if okB && e.O("pos") != nil {
			e.O("pos").SetF("x", mb[0])
			e.O("pos").SetF("y", mb[1])
		}
		if c := o.O("controlPos1"); c != nil && okA {
			setR(c, T.Apply(smap.P(c)))
		}
		if c := o.O("controlPos2"); c != nil && okB {
			setR(c, T.Apply(smap.P(c)))
		}
		if okA && okB {
			rep.PathsInside++
		} else if okA || okB {
			rep.PathsCrossing = append(rep.PathsCrossing, o.S("instanceName"))
		}
	}
	// ---- feature lines
	onKept := func(q smap.Pt) bool {
		c := cellOf(q.X, q.Y, 0.05)
		for i := -2; i <= 2; i++ {
			for j := -2; j <= 2; j++ {
				if _, ok := keptOld[cell{c[0] + i, c[1] + j}]; ok {
					return true
				}
			}
		}
		return false
	}
	lines := m.Lines()
	rep.LinesTotal = len(lines)
	for _, l := range lines {
		if !in(l.Mid()) {
			continue
		}
		if p.Lines != "all" {
			// a line stays only if it sits on an anchored wall that stayed; otherwise it moves with its walls
			n := 0
			for _, q := range []smap.Pt{l.Mid(), l.A, l.B} {
				if onKept(q) {
					n++
				}
			}
			if n >= 2 {
				rep.LinesKept++
				continue
			}
		}
		ln := l.O.O("line")
		setR(ln.O("startPos"), T.Apply(l.A))
		setR(ln.O("endPos"), T.Apply(l.B))
		var px, py *smap.Obj
		for _, pr := range smap.Objs(l.O.A("property")) {
			switch pr.S("key") {
			case "directionPosX":
				if px == nil {
					px = pr
				}
			case "directionPosY":
				if py == nil {
					py = pr
				}
			}
		}
		if px != nil && py != nil {
			d := T.Apply(smap.Pt{X: px.F("doubleValue"), Y: py.F("doubleValue")})
			for _, e := range []struct {
				o *smap.Obj
				v float64
			}{{px, smap.R3(d.X)}, {py, smap.R3(d.Y)}} {
				e.o.SetF("doubleValue", e.v)
				e.o.Set("value", base64.StdEncoding.EncodeToString([]byte(smap.FmtNum(e.v))))
			}
		}
		rep.LinesMoved++
	}
	// ---- areas: an area moves when its centre is in the region
	for _, a := range m.List("advancedAreaList") {
		pg := smap.Objs(a.A("posGroup"))
		if len(pg) == 0 {
			continue
		}
		var cx, cy float64
		for _, q := range pg {
			cx += q.F("x")
			cy += q.F("y")
		}
		if !in(smap.Pt{X: cx / float64(len(pg)), Y: cy / float64(len(pg))}) {
			continue
		}
		for _, q := range pg {
			setR(q, T.Apply(smap.P(q)))
		}
		if a.IsNum("dir") {
			a.SetF("dir", a.F("dir")+T.Th)
		}
		rep.AreasMoved = append(rep.AreasMoved, a.S("className")+" "+a.S("instanceName"))
	}
	// ---- bins follow their station
	for _, g := range m.List("binLocationsList") {
		for _, bl := range smap.Objs(g.A("binLocationList")) {
			if _, ok := rep.Moved[bl.S("pointName")]; !ok {
				continue
			}
			if pos := bl.O("pos"); pos != nil {
				setR(pos, T.Apply(smap.P(pos)))
			}
			for _, pr := range smap.Objs(bl.A("property")) {
				if pr.S("key") != "points" || pr.S("stringValue") == "" {
					continue
				}
				v, err := smap.Parse([]byte(pr.S("stringValue")))
				arr, ok := v.([]any)
				if err != nil || !ok {
					continue
				}
				for _, q := range smap.Objs(arr) {
					n := T.Apply(smap.P(q))
					q.SetF("x", n.X)
					q.SetF("y", n.Y)
				}
				pr.Set("stringValue", string(smap.Marshal(arr)))
			}
			rep.BinsMoved++
		}
	}
	m.ResetCache()
	FixHeader(m, p.MapName)
	sort.Strings(rep.PathsCrossing)
	return rep, nil
}

// FixHeader grows the header bounds to cover all walls and optionally renames the map.
func FixHeader(m *smap.Map, name string) {
	h := m.Root.O("header")
	if h == nil {
		return
	}
	mn, mx := h.O("minPos"), h.O("maxPos")
	if mn != nil && mx != nil && len(m.Walls()) > 0 {
		x0, y0, x1, y1 := mn.F("x"), mn.F("y"), mx.F("x"), mx.F("y")
		for _, w := range m.Walls() {
			x0, y0, x1, y1 = math.Min(x0, w.X), math.Min(y0, w.Y), math.Max(x1, w.X), math.Max(y1, w.Y)
		}
		set := func(o *smap.Obj, k string, v float64) {
			if o.F(k) != v {
				o.SetF(k, v)
			}
		}
		set(mn, "x", x0)
		set(mn, "y", y0)
		set(mx, "x", x1)
		set(mx, "y", y1)
	}
	if name != "" {
		h.Set("mapName", name)
	}
}

// BoxReport lists what MergeBox replaced.
type BoxReport struct {
	WallsRemoved, WallsAdded int
	RssiRemoved, RssiAdded   int
	LinesRemoved, LinesAdded int
}

// MergeBox replaces walls, reflection points and feature lines inside a box with those of a donor map.
// Stations, paths, areas and bins are not touched.
func MergeBox(m, donor *smap.Map, x0, x1, y0, y1 float64) *BoxReport {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	in := func(q smap.Pt) bool { return q.X >= x0 && q.X <= x1 && q.Y >= y0 && q.Y <= y1 }
	rep := &BoxReport{}
	swap := func(key string, at func(*smap.Obj) smap.Pt) (removed, added int) {
		var keep []any
		for _, v := range m.Root.A(key) {
			if o, ok := v.(*smap.Obj); ok && in(at(o)) {
				removed++
				continue
			}
			keep = append(keep, v)
		}
		for _, v := range donor.Root.A(key) {
			if o, ok := v.(*smap.Obj); ok && in(at(o)) {
				keep = append(keep, smap.Clone(v))
				added++
			}
		}
		if keep == nil {
			keep = []any{}
		}
		if m.Root.Has(key) || added > 0 {
			m.Root.Set(key, keep)
		}
		return
	}
	mid := func(o *smap.Obj) smap.Pt {
		l := o.O("line")
		a, b := smap.P(l.O("startPos")), smap.P(l.O("endPos"))
		return smap.Pt{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
	}
	rep.WallsRemoved, rep.WallsAdded = swap("normalPosList", smap.P)
	rep.RssiRemoved, rep.RssiAdded = swap("rssiPosList", smap.P)
	rep.LinesRemoved, rep.LinesAdded = swap("advancedLineList", mid)
	m.ResetCache()
	FixHeader(m, "")
	return rep
}

// RemoveLines deletes feature lines by their index in the file. It returns how many were removed.
func RemoveLines(m *smap.Map, idx []int) int {
	del := map[int]bool{}
	for _, i := range idx {
		del[i] = true
	}
	var keep []any
	n, k := 0, 0
	for _, v := range m.Root.A("advancedLineList") {
		if _, ok := v.(*smap.Obj); ok {
			if del[k] {
				n++
				k++
				continue
			}
			k++
		}
		keep = append(keep, v)
	}
	if keep == nil {
		keep = []any{}
	}
	m.Root.Set("advancedLineList", keep)
	return n
}
