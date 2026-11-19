package mapedit

import (
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"drishtimap/smap"
)

// EditOp is one hand edit. Edits are applied in order to a copy of the base map; the result is
// written as a candidate file. Nothing here touches a robot.
type EditOp struct {
	Op        string      `json:"op"`
	Lines     []int       `json:"lines,omitempty"`   // feature line numbers (in the map as it is at this step)
	At        []float64   `json:"at,omitempty"`      // [x,y]: the nearest feature line
	Box       []float64   `json:"box,omitempty"`     // [x0,y0,x1,y1]
	Region    [][]float64 `json:"region,omitempty"`  // polygon corners
	Path      string      `json:"path,omitempty"`    // path name, e.g. LM517-LM518
	Paths     []string    `json:"paths,omitempty"`   // several path names
	Station   string      `json:"station,omitempty"` // station name
	Key       string      `json:"key,omitempty"`     // property name (direction, maxspeed, spin ...)
	Value     any         `json:"value,omitempty"`   // new property value
	X         *float64    `json:"x,omitempty"`       // new position
	Y         *float64    `json:"y,omitempty"`
	Dx        float64     `json:"dx,omitempty"` // or a shift
	Dy        float64     `json:"dy,omitempty"`
	DirDeg    *float64    `json:"dirDeg,omitempty"`    // station direction in degrees
	IgnoreDir *bool       `json:"ignoreDir,omitempty"` // station "ignore direction" flag
	RotDeg    float64     `json:"rotDeg,omitempty"`    // move_walls: turn in degrees
	Pivot     []float64   `json:"pivot,omitempty"`     // move_walls: point the turn is about (default: origin)
	Tx        float64     `json:"tx,omitempty"`        // move_walls: shift after the turn
	Ty        float64     `json:"ty,omitempty"`
	Donor     string      `json:"donor,omitempty"`     // take_walls: map to copy from
	Mode      string      `json:"mode,omitempty"`      // take_walls: "replace" (default) or "add"
	WithLines bool        `json:"withLines,omitempty"` // take_walls: also take the donor's feature lines
	Rssi      *bool       `json:"rssi,omitempty"`      // erase/move/take walls: include reflection points (default true)
	WithPaths bool        `json:"withPaths,omitempty"` // delete_station: also delete its paths
	Name      string      `json:"name,omitempty"`      // add_station: new name
	Like      string      `json:"like,omitempty"`      // add_station / add_path: existing object to copy type and properties from
	From      string      `json:"from,omitempty"`      // add_path
	To        string      `json:"to,omitempty"`
}

// OpNames lists the edit operations with a one-line meaning (shown in the app and to the AI model).
var OpNames = [][2]string{
	{"delete_lines", "remove feature lines: lines=[numbers], or at=[x,y] (nearest line), or every line in box/region"},
	{"erase_walls", "remove wall dots (and reflection points) inside box/region"},
	{"move_walls", "turn/shift only the wall dots inside box/region: rotDeg about pivot=[x,y], then tx, ty"},
	{"take_walls", "inside box/region use the walls of another map: donor, mode replace|add, withLines"},
	{"set_path", "set a property on path/paths (or all paths inside box/region): key (direction, maxspeed ...), value"},
	{"move_station", "move one station to x,y (or by dx,dy); its paths and bin follow; optional dirDeg"},
	{"set_station", "set a station's dirDeg, ignoreDir, or a property (key, value)"},
	{"delete_path", "remove path/paths"},
	{"delete_station", "remove a station (withPaths=true also removes its paths)"},
	{"add_station", "new station: name, x, y, optional dirDeg, like = existing station to copy type and properties from"},
	{"add_path", "new straight path: from, to, optional like = existing path to copy type and properties from"},
}

// Editor applies edits to one map and remembers what was changed on purpose.
type Editor struct {
	M        *smap.Map
	Donor    func(name string) (*smap.Map, error)
	Intended map[string]bool // stations and paths changed by hand (the checks do not compare these with the base)
	Log      []string        // one line per applied edit
}

func NewEditor(m *smap.Map, donor func(string) (*smap.Map, error)) *Editor {
	return &Editor{M: m, Donor: donor, Intended: map[string]bool{}}
}

// Apply runs one edit. On error the map is unchanged.
func (e *Editor) Apply(op EditOp) (string, error) {
	note, err := e.apply(op)
	if err != nil {
		return "", err
	}
	e.M.ResetCache()
	e.Log = append(e.Log, note)
	return note, nil
}

func (op *EditOp) area() (func(smap.Pt) bool, string, bool) {
	if len(op.Region) >= 3 {
		var pg []smap.Pt
		for _, q := range op.Region {
			if len(q) == 2 {
				pg = append(pg, smap.Pt{X: q[0], Y: q[1]})
			}
		}
		if len(pg) >= 3 {
			return func(p smap.Pt) bool { return smap.InPoly(p, pg) }, fmt.Sprintf("region of %d corners around (%.1f, %.1f)", len(pg), pg[0].X, pg[0].Y), true
		}
	}
	if len(op.Box) == 4 {
		x0, x1 := math.Min(op.Box[0], op.Box[2]), math.Max(op.Box[0], op.Box[2])
		y0, y1 := math.Min(op.Box[1], op.Box[3]), math.Max(op.Box[1], op.Box[3])
		return func(p smap.Pt) bool { return p.X >= x0 && p.X <= x1 && p.Y >= y0 && p.Y <= y1 }, fmt.Sprintf("box x %.2f..%.2f, y %.2f..%.2f", x0, x1, y0, y1), true
	}
	return nil, "", false
}

func (e *Editor) station(name string) *smap.Obj {
	for _, o := range e.M.List("advancedPointList") {
		if strings.EqualFold(o.S("instanceName"), name) {
			return o
		}
	}
	return nil
}

func (e *Editor) path(name string) *smap.Obj {
	for _, p := range e.M.Paths() { // Paths gives every path a unique name, also in maps that name them all alike
		if strings.EqualFold(p.Name, name) {
			return p.O
		}
	}
	return nil
}

func findProp(o *smap.Obj, key string) *smap.Obj {
	for _, p := range smap.Objs(o.A("property")) {
		if p.S("key") == key {
			return p
		}
	}
	return nil
}

// setProp writes a property value in the file's own encoding (typed field + base64 text).
func setProp(p *smap.Obj, v any) (string, error) {
	var text string
	switch t := p.S("type"); t {
	case "int", "int32", "uint32", "int64", "uint64":
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return "", fmt.Errorf("property %s needs a whole number", p.S("key"))
		}
		field := "int32Value"
		for _, k := range []string{"int32Value", "uint32Value", "int64Value", "uint64Value"} {
			if p.Has(k) {
				field = k
			}
		}
		text = strconv.FormatInt(int64(f), 10)
		p.Set(field, smap.Num(text))
	case "double", "float":
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("property %s needs a number", p.S("key"))
		}
		field := "doubleValue"
		if p.Has("floatValue") {
			field = "floatValue"
		}
		text = string(smap.FmtNum(f))
		p.Set(field, smap.FmtNum(f))
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("property %s needs true or false", p.S("key"))
		}
		text = strconv.FormatBool(b)
		p.Set("boolValue", b)
	case "string":
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("property %s needs text", p.S("key"))
		}
		text = s
		p.Set("stringValue", s)
	default:
		return "", fmt.Errorf("property %s has type %q, which this tool does not edit", p.S("key"), t)
	}
	if p.Has("value") {
		p.Set("value", base64.StdEncoding.EncodeToString([]byte(text)))
	}
	return text, nil
}

func setXY(o *smap.Obj, p smap.Pt) { o.SetF("x", smap.R3(p.X)); o.SetF("y", smap.R3(p.Y)) }

// straighten puts the control points of a straight path at 1/3 and 2/3 of its chord.
func straighten(o *smap.Obj) {
	s, t := o.O("startPos"), o.O("endPos")
	c1, c2 := o.O("controlPos1"), o.O("controlPos2")
	if s == nil || t == nil || s.O("pos") == nil || t.O("pos") == nil || c1 == nil || c2 == nil {
		return
	}
	a, b := smap.P(s.O("pos")), smap.P(t.O("pos"))
	setXY(c1, smap.Pt{X: a.X + (b.X-a.X)/3, Y: a.Y + (b.Y-a.Y)/3})
	setXY(c2, smap.Pt{X: a.X + 2*(b.X-a.X)/3, Y: a.Y + 2*(b.Y-a.Y)/3})
}

func filterList(m *smap.Map, key string, drop func(*smap.Obj) bool) int {
	if !m.Root.Has(key) {
		return 0
	}
	keep := []any{}
	n := 0
	for _, v := range m.Root.A(key) {
		if o, ok := v.(*smap.Obj); ok && drop(o) {
			n++
			continue
		}
		keep = append(keep, v)
	}
	if n > 0 {
		m.Root.Set(key, keep)
	}
	return n
}

var stationName = regexp.MustCompile(`^[A-Za-z]{1,6}[0-9]{1,6}$`)

func (e *Editor) apply(op EditOp) (string, error) {
	m := e.M
	in, where, hasArea := op.area()
	rssi := op.Rssi == nil || *op.Rssi
	switch op.Op {
	case "delete_lines":
		lines := m.Lines()
		pick := map[int]bool{}
		for _, i := range op.Lines {
			if i < 0 || i >= len(lines) {
				return "", fmt.Errorf("there is no feature line #%d (the map has %d)", i, len(lines))
			}
			pick[i] = true
		}
		if len(op.At) == 2 {
			p, best, bi := smap.Pt{X: op.At[0], Y: op.At[1]}, 0.5, -1
			for _, l := range lines {
				if d := distSeg(p, l.A, l.B); d < best {
					best, bi = d, l.Idx
				}
			}
			if bi < 0 {
				return "", fmt.Errorf("no feature line within 0.5 m of (%.2f, %.2f)", p.X, p.Y)
			}
			pick[bi] = true
		}
		if hasArea && len(op.Lines) == 0 && len(op.At) == 0 {
			for _, l := range lines {
				if in(l.Mid()) {
					pick[l.Idx] = true
				}
			}
		}
		if len(pick) == 0 {
			return "", fmt.Errorf("no feature line selected")
		}
		var idx []int
		var desc []string
		for i := range pick {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for k, i := range idx {
			if k < 6 {
				desc = append(desc, fmt.Sprintf("(%.2f,%.2f)-(%.2f,%.2f)", lines[i].A.X, lines[i].A.Y, lines[i].B.X, lines[i].B.Y))
			}
		}
		if len(idx) > 6 {
			desc = append(desc, fmt.Sprintf("and %d more", len(idx)-6))
		}
		n := RemoveLines(m, idx)
		return fmt.Sprintf("removed %d feature line(s): %s", n, strings.Join(desc, " ")), nil

	case "erase_walls":
		if !hasArea {
			return "", fmt.Errorf("erase_walls needs a box or a region")
		}
		total, hit := 0, 0
		for _, o := range m.List("normalPosList") {
			total++
			if in(smap.P(o)) {
				hit++
			}
		}
		if hit == 0 {
			return "", fmt.Errorf("no wall dots inside that %s", where)
		}
		if hit*2 > total {
			return "", fmt.Errorf("that would erase %d of %d wall dots (more than half the map); draw a smaller area", hit, total)
		}
		n := filterList(m, "normalPosList", func(o *smap.Obj) bool { return in(smap.P(o)) })
		r := 0
		if rssi {
			r = filterList(m, "rssiPosList", func(o *smap.Obj) bool { return in(smap.P(o)) })
		}
		return fmt.Sprintf("erased %d wall dots and %d reflection points in %s", n, r, where), nil

	case "move_walls":
		if !hasArea {
			return "", fmt.Errorf("move_walls needs a box or a region")
		}
		th := op.RotDeg * math.Pi / 180
		if math.Abs(op.RotDeg) > 20 || math.Abs(op.Tx) > 30 || math.Abs(op.Ty) > 30 {
			return "", fmt.Errorf("a turn of %.1f deg / shift of %.1f, %.1f m is too large for a map correction", op.RotDeg, op.Tx, op.Ty)
		}
		if op.RotDeg == 0 && op.Tx == 0 && op.Ty == 0 {
			return "", fmt.Errorf("move_walls needs rotDeg, tx or ty")
		}
		T := smap.Rigid{Th: th, Tx: op.Tx, Ty: op.Ty}
		if len(op.Pivot) == 2 { // turn about the pivot, then shift
			c, s := math.Cos(th), math.Sin(th)
			px, py := op.Pivot[0], op.Pivot[1]
			T.Tx += px - (c*px - s*py)
			T.Ty += py - (s*px + c*py)
		}
		n, r := 0, 0
		for _, o := range m.List("normalPosList") {
			if q := smap.P(o); in(q) {
				setXY(o, T.Apply(q))
				n++
			}
		}
		if rssi {
			for _, o := range m.List("rssiPosList") {
				if q := smap.P(o); in(q) {
					setXY(o, T.Apply(q))
					r++
				}
			}
		}
		if n == 0 {
			return "", fmt.Errorf("no wall dots inside that %s", where)
		}
		return fmt.Sprintf("moved %d wall dots and %d reflection points in %s: turn %.3f deg, shift %.3f, %.3f m (stations, paths and lines not moved)", n, r, where, op.RotDeg, T.Tx, T.Ty), nil

	case "take_walls":
		if !hasArea {
			return "", fmt.Errorf("take_walls needs a box or a region")
		}
		if op.Donor == "" || e.Donor == nil {
			return "", fmt.Errorf("take_walls needs donor (the map to copy walls from)")
		}
		d, err := e.Donor(op.Donor)
		if err != nil {
			return "", err
		}
		add := op.Mode == "add"
		if op.Mode != "" && op.Mode != "add" && op.Mode != "replace" {
			return "", fmt.Errorf("mode must be replace or add")
		}
		swap := func(key string, at func(*smap.Obj) smap.Pt) (removed, added int) {
			have := map[[2]int64]bool{}
			if add {
				for _, o := range m.List(key) {
					q := at(o)
					have[[2]int64{int64(math.Round(q.X * 1000)), int64(math.Round(q.Y * 1000))}] = true
				}
			} else {
				removed = filterList(m, key, func(o *smap.Obj) bool { return in(at(o)) })
			}
			list := m.Root.A(key)
			for _, v := range d.Root.A(key) {
				o, ok := v.(*smap.Obj)
				if !ok || !in(at(o)) {
					continue
				}
				q := at(o)
				if add && have[[2]int64{int64(math.Round(q.X * 1000)), int64(math.Round(q.Y * 1000))}] {
					continue
				}
				list = append(list, smap.Clone(v))
				added++
			}
			if added > 0 || m.Root.Has(key) {
				if list == nil {
					list = []any{}
				}
				m.Root.Set(key, list)
			}
			return
		}
		mid := func(o *smap.Obj) smap.Pt {
			l := o.O("line")
			a, b := smap.P(l.O("startPos")), smap.P(l.O("endPos"))
			return smap.Pt{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
		}
		wr, wa := swap("normalPosList", smap.P)
		rr, ra, lr, la := 0, 0, 0, 0
		if rssi {
			rr, ra = swap("rssiPosList", smap.P)
		}
		if op.WithLines {
			lr, la = swap("advancedLineList", mid)
		}
		if add && wa+ra+la == 0 {
			return "", fmt.Errorf("nothing to add: %s has no walls in that %s that this map lacks", op.Donor, where)
		}
		if wa+wr+la+lr == 0 {
			return "", fmt.Errorf("neither map has walls inside that %s", where)
		}
		return fmt.Sprintf("walls in %s taken from %s (%s): wall dots -%d +%d, reflection points -%d +%d, feature lines -%d +%d", where, op.Donor, map[bool]string{true: "added to the existing ones", false: "replacing the existing ones"}[add], wr, wa, rr, ra, lr, la), nil

	case "set_path":
		if op.Key == "" || op.Value == nil {
			return "", fmt.Errorf("set_path needs key and value")
		}
		names := append([]string{}, op.Paths...)
		if op.Path != "" {
			names = append(names, op.Path)
		}
		var objs []*smap.Obj
		for _, n := range names {
			o := e.path(n)
			if o == nil {
				return "", fmt.Errorf("there is no path %q", n)
			}
			objs = append(objs, o)
		}
		if len(names) == 0 && hasArea {
			for _, p := range m.Paths() {
				if in(p.P0) && in(p.P3) {
					objs = append(objs, p.O)
				}
			}
		}
		if len(objs) == 0 {
			return "", fmt.Errorf("no path selected")
		}
		// a property the path does not carry yet is copied from another path that has it
		var tmpl *smap.Obj
		for _, o := range m.List("advancedCurveList") {
			if p := findProp(o, op.Key); p != nil {
				tmpl = p
				break
			}
		}
		if tmpl == nil {
			return "", fmt.Errorf("no path in this map has a property %q; known path properties are direction, movestyle, maxspeed and the obstacle distances", op.Key)
		}
		// validate on a copy first so a bad value changes nothing
		if _, err := setProp(smap.Clone(tmpl).(*smap.Obj), op.Value); err != nil {
			return "", err
		}
		var done []string
		text := ""
		for _, o := range objs {
			p := findProp(o, op.Key)
			old := "not set"
			if p == nil {
				p = smap.Clone(tmpl).(*smap.Obj)
				o.Set("property", append(o.A("property"), p))
			} else {
				old = propVal(p)
			}
			text, _ = setProp(p, op.Value)
			e.Intended[o.S("instanceName")] = true
			if len(done) < 12 {
				done = append(done, fmt.Sprintf("%s (was %s)", o.S("instanceName"), old))
			}
		}
		if len(objs) > 12 {
			done = append(done, fmt.Sprintf("and %d more", len(objs)-12))
		}
		return fmt.Sprintf("path property %s = %s on %d path(s): %s", op.Key, text, len(objs), strings.Join(done, ", ")), nil

	case "move_station":
		o := e.station(op.Station)
		if o == nil || o.O("pos") == nil {
			return "", fmt.Errorf("there is no station %q", op.Station)
		}
		name := o.S("instanceName")
		old := smap.P(o.O("pos"))
		nw := smap.Pt{X: old.X + op.Dx, Y: old.Y + op.Dy}
		if op.X != nil && op.Y != nil {
			nw = smap.Pt{X: *op.X, Y: *op.Y}
		}
		nw = smap.Pt{X: smap.R3(nw.X), Y: smap.R3(nw.Y)}
		d := smap.Pt{X: nw.X - old.X, Y: nw.Y - old.Y}
		dist := math.Hypot(d.X, d.Y)
		if dist > 30 || math.IsNaN(dist) {
			return "", fmt.Errorf("that moves %s by %.1f m; too far for a correction", name, dist)
		}
		if dist == 0 && op.DirDeg == nil {
			return "", fmt.Errorf("%s is already there", name)
		}
		setXY(o.O("pos"), nw)
		if op.DirDeg != nil {
			o.SetF("dir", *op.DirDeg*math.Pi/180)
		}
		e.Intended[name] = true
		np := 0
		for _, c := range m.List("advancedCurveList") {
			s, t := c.O("startPos"), c.O("endPos")
			hit := false
			if s != nil && s.S("instanceName") == name && s.O("pos") != nil {
				setXY(s.O("pos"), nw)
				if c.S("className") == "BezierPath" && c.O("controlPos1") != nil {
					q := smap.P(c.O("controlPos1"))
					setXY(c.O("controlPos1"), smap.Pt{X: q.X + d.X, Y: q.Y + d.Y})
				}
				hit = true
			}
			if t != nil && t.S("instanceName") == name && t.O("pos") != nil {
				setXY(t.O("pos"), nw)
				if c.S("className") == "BezierPath" && c.O("controlPos2") != nil {
					q := smap.P(c.O("controlPos2"))
					setXY(c.O("controlPos2"), smap.Pt{X: q.X + d.X, Y: q.Y + d.Y})
				}
				hit = true
			}
			if hit {
				if c.S("className") != "BezierPath" {
					straighten(c)
				}
				e.Intended[c.S("instanceName")] = true
				np++
			}
		}
		nb := 0
		for _, g := range m.List("binLocationsList") {
			for _, bl := range smap.Objs(g.A("binLocationList")) {
				if bl.S("pointName") != name {
					continue
				}
				if pos := bl.O("pos"); pos != nil {
					q := smap.P(pos)
					setXY(pos, smap.Pt{X: q.X + d.X, Y: q.Y + d.Y})
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
						q.SetF("x", q.F("x")+d.X)
						q.SetF("y", q.F("y")+d.Y)
					}
					pr.Set("stringValue", string(smap.Marshal(arr)))
				}
				nb++
			}
		}
		return fmt.Sprintf("station %s moved %.3f m: (%.3f, %.3f) -> (%.3f, %.3f); %d path(s) and %d bin(s) follow", name, dist, old.X, old.Y, nw.X, nw.Y, np, nb), nil

	case "set_station":
		o := e.station(op.Station)
		if o == nil {
			return "", fmt.Errorf("there is no station %q", op.Station)
		}
		if op.DirDeg == nil && op.IgnoreDir == nil && op.Key == "" {
			return "", fmt.Errorf("set_station needs dirDeg, ignoreDir, or key and value")
		}
		var done []string
		if op.Key != "" {
			p := findProp(o, op.Key)
			if p == nil {
				return "", fmt.Errorf("station %s has no property %q", o.S("instanceName"), op.Key)
			}
			old := propVal(p)
			if _, err := setProp(smap.Clone(p).(*smap.Obj), op.Value); err != nil {
				return "", err
			}
			t, _ := setProp(p, op.Value)
			done = append(done, fmt.Sprintf("%s %s -> %s", op.Key, old, t))
		}
		if op.DirDeg != nil {
			old := "none"
			if o.IsNum("dir") {
				old = fmt.Sprintf("%.1f", o.F("dir")*180/math.Pi)
			}
			o.SetF("dir", *op.DirDeg*math.Pi/180)
			done = append(done, fmt.Sprintf("direction %s -> %.1f deg", old, *op.DirDeg))
		}
		if op.IgnoreDir != nil {
			if *op.IgnoreDir {
				o.Set("ignoreDir", true)
			} else {
				o.Del("ignoreDir")
			}
			done = append(done, fmt.Sprintf("ignore direction = %v", *op.IgnoreDir))
		}
		e.Intended[o.S("instanceName")] = true
		return fmt.Sprintf("station %s: %s", o.S("instanceName"), strings.Join(done, ", ")), nil

	case "delete_path":
		names := append([]string{}, op.Paths...)
		if op.Path != "" {
			names = append(names, op.Path)
		}
		if len(names) == 0 {
			return "", fmt.Errorf("delete_path needs path")
		}
		del := map[string]bool{}
		for _, n := range names {
			o := e.path(n)
			if o == nil {
				return "", fmt.Errorf("there is no path %q", n)
			}
			del[o.S("instanceName")] = true
		}
		n := filterList(m, "advancedCurveList", func(o *smap.Obj) bool { return del[o.S("instanceName")] })
		for k := range del {
			e.Intended[k] = true
		}
		return fmt.Sprintf("removed %d path(s): %s", n, strings.Join(sortedKeys(del), ", ")), nil

	case "delete_station":
		o := e.station(op.Station)
		if o == nil {
			return "", fmt.Errorf("there is no station %q", op.Station)
		}
		name := o.S("instanceName")
		for _, g := range m.List("binLocationsList") {
			for _, bl := range smap.Objs(g.A("binLocationList")) {
				if bl.S("pointName") == name {
					return "", fmt.Errorf("station %s carries bin %s; remove the bin in RoboShop first", name, bl.S("instanceName"))
				}
			}
		}
		att := map[string]bool{}
		for _, p := range m.Paths() {
			if p.From == name || p.To == name {
				att[p.Name] = true
			}
		}
		if len(att) > 0 && !op.WithPaths {
			return "", fmt.Errorf("station %s has %d path(s): %s. Set withPaths to remove them too", name, len(att), strings.Join(sortedKeys(att), ", "))
		}
		filterList(m, "advancedCurveList", func(c *smap.Obj) bool { return att[c.S("instanceName")] })
		filterList(m, "advancedPointList", func(s *smap.Obj) bool { return s.S("instanceName") == name })
		e.Intended[name] = true
		for k := range att {
			e.Intended[k] = true
		}
		return fmt.Sprintf("removed station %s and %d path(s) %s", name, len(att), strings.Join(sortedKeys(att), ", ")), nil

	case "add_station":
		if !stationName.MatchString(op.Name) {
			return "", fmt.Errorf("station name %q is not valid (letters then digits, for example LM700)", op.Name)
		}
		if e.station(op.Name) != nil {
			return "", fmt.Errorf("station %s already exists", op.Name)
		}
		if op.X == nil || op.Y == nil {
			return "", fmt.Errorf("add_station needs x and y")
		}
		prefix := strings.TrimRight(strings.ToUpper(op.Name), "0123456789")
		var tmpl *smap.Obj
		if op.Like != "" {
			if tmpl = e.station(op.Like); tmpl == nil {
				return "", fmt.Errorf("there is no station %q to copy from", op.Like)
			}
		} else {
			for _, s := range m.List("advancedPointList") { // the plainest station with the same prefix
				if strings.TrimRight(strings.ToUpper(s.S("instanceName")), "0123456789") == prefix && (tmpl == nil || len(s.A("property")) < len(tmpl.A("property"))) {
					tmpl = s
				}
			}
			if tmpl == nil {
				return "", fmt.Errorf("no station starting with %s exists to copy the type from; give like", prefix)
			}
		}
		if tp := strings.TrimRight(strings.ToUpper(tmpl.S("instanceName")), "0123456789"); tp != prefix {
			return "", fmt.Errorf("%s is a %s (%s...); a station named %s must be copied from a %s station", tmpl.S("instanceName"), tmpl.S("className"), tp, op.Name, prefix)
		}
		n := smap.Clone(tmpl).(*smap.Obj)
		n.Set("instanceName", strings.ToUpper(op.Name))
		setXY(n.O("pos"), smap.Pt{X: *op.X, Y: *op.Y})
		if op.DirDeg != nil {
			n.SetF("dir", *op.DirDeg*math.Pi/180)
			n.Del("ignoreDir")
		}
		m.Root.Set("advancedPointList", append(m.Root.A("advancedPointList"), n))
		e.Intended[n.S("instanceName")] = true
		return fmt.Sprintf("added station %s (%s, properties copied from %s) at (%.3f, %.3f). It must also exist in the fleet scene before a task can use it", n.S("instanceName"), n.S("className"), tmpl.S("instanceName"), smap.R3(*op.X), smap.R3(*op.Y)), nil

	case "add_path":
		a, b := e.station(op.From), e.station(op.To)
		if a == nil || b == nil {
			return "", fmt.Errorf("add_path needs two existing stations (from, to)")
		}
		name := a.S("instanceName") + "-" + b.S("instanceName")
		if a == b {
			return "", fmt.Errorf("a path needs two different stations")
		}
		if e.path(name) != nil {
			return "", fmt.Errorf("path %s already exists", name)
		}
		var tmpl *smap.Obj
		if op.Like != "" {
			if tmpl = e.path(op.Like); tmpl == nil {
				return "", fmt.Errorf("there is no path %q to copy from", op.Like)
			}
			if tmpl.S("className") == "BezierPath" {
				return "", fmt.Errorf("%s is a curved path; copy from a straight one", op.Like)
			}
		} else {
			for _, c := range m.List("advancedCurveList") { // the plainest straight path
				if c.S("className") != "BezierPath" && c.O("controlPos1") != nil && (tmpl == nil || len(c.A("property")) < len(tmpl.A("property"))) {
					tmpl = c
				}
			}
			if tmpl == nil {
				return "", fmt.Errorf("the map has no straight path to copy the type from")
			}
		}
		n := smap.Clone(tmpl).(*smap.Obj)
		n.Set("instanceName", name)
		for _, x := range []struct {
			k string
			s *smap.Obj
		}{{"startPos", a}, {"endPos", b}} {
			o := n.O(x.k)
			o.Set("instanceName", x.s.S("instanceName"))
			setXY(o.O("pos"), smap.P(x.s.O("pos")))
		}
		straighten(n)
		m.Root.Set("advancedCurveList", append(m.Root.A("advancedCurveList"), n))
		e.Intended[name] = true
		pa, pb := smap.P(a.O("pos")), smap.P(b.O("pos"))
		return fmt.Sprintf("added path %s (%s, properties copied from %s), %.2f m, one way from %s to %s. It must also exist in the fleet scene", name, n.S("className"), tmpl.S("instanceName"), math.Hypot(pa.X-pb.X, pa.Y-pb.Y), a.S("instanceName"), b.S("instanceName")), nil
	}
	var known []string
	for _, o := range OpNames {
		known = append(known, o[0])
	}
	return "", fmt.Errorf("unknown edit %q; edits are: %s", op.Op, strings.Join(known, ", "))
}

func sortedKeys(m map[string]bool) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

// Structure lists faults in how stations, paths and bins refer to each other.
func Structure(m *smap.Map) []string {
	var out []string
	pos := map[string]smap.Pt{}
	for _, s := range m.Stations() {
		if _, dup := pos[s.Name]; dup {
			out = append(out, "two stations are named "+s.Name)
		}
		pos[s.Name] = s.Pos
	}
	seen := map[string]bool{}
	for _, p := range m.Paths() {
		if seen[p.Name] {
			out = append(out, "more than one path runs from "+p.From+" to "+p.To+" ("+p.Name+")")
		}
		seen[p.Name] = true
		for _, e := range []struct {
			n string
			p smap.Pt
		}{{p.From, p.P0}, {p.To, p.P3}} {
			s, ok := pos[e.n]
			if !ok {
				out = append(out, fmt.Sprintf("path %s refers to station %s, which does not exist", p.Name, e.n))
			} else if math.Hypot(s.X-e.p.X, s.Y-e.p.Y) > 0.002 {
				out = append(out, fmt.Sprintf("path %s does not end on station %s (%.3f m off)", p.Name, e.n, math.Hypot(s.X-e.p.X, s.Y-e.p.Y)))
			}
		}
	}
	for _, g := range m.List("binLocationsList") {
		for _, bl := range smap.Objs(g.A("binLocationList")) {
			if n := bl.S("pointName"); n != "" {
				if _, ok := pos[n]; !ok {
					out = append(out, fmt.Sprintf("bin %s refers to station %s, which does not exist", bl.S("instanceName"), n))
				}
			}
		}
	}
	sort.Strings(out)
	uniq := out[:0]
	for i, x := range out {
		if i == 0 || x != out[i-1] {
			uniq = append(uniq, x)
		}
	}
	return uniq
}
