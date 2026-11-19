package smap

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// Pt is a point in map coordinates (metres).
type Pt struct{ X, Y float64 }

// Map is one loaded .smap file.
type Map struct {
	Root   *Obj
	File   string
	MD5    string
	SHA256 string
	Size   int
	walls  []Pt
}

// Load reads and parses a .smap file.
func Load(path string) (*Map, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := FromBytes(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	m.File = path
	return m, nil
}

// FromBytes parses map JSON.
func FromBytes(b []byte) (*Map, error) {
	v, err := Parse(b)
	if err != nil {
		return nil, err
	}
	root, ok := v.(*Obj)
	if !ok || root.Get("header") == nil {
		return nil, fmt.Errorf("not a .smap file (no header)")
	}
	h1, h2 := md5.Sum(b), sha256.Sum256(b)
	return &Map{Root: root, MD5: hex.EncodeToString(h1[:]), SHA256: hex.EncodeToString(h2[:]), Size: len(b)}, nil
}

// Bytes returns the file content for the current tree.
func (m *Map) Bytes() []byte { return Marshal(m.Root) }

// Save writes the map. It refuses to overwrite an existing file: candidates never replace a source map.
func (m *Map) Save(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; choose another name", filepath.Base(path))
	}
	b := m.Bytes()
	if err := os.WriteFile(path, b, 0644); err != nil {
		return err
	}
	h1, h2 := md5.Sum(b), sha256.Sum256(b)
	m.File, m.MD5, m.SHA256, m.Size = path, hex.EncodeToString(h1[:]), hex.EncodeToString(h2[:]), len(b)
	return nil
}

// Clone copies the whole map so it can be edited without touching the original.
func (m *Map) Clone() *Map { return &Map{Root: Clone(m.Root).(*Obj), File: m.File} }

// Name is the map name stored in the header.
func (m *Map) Name() string { return m.Root.O("header").S("mapName") }

// P reads {x,y} from an object.
func P(o *Obj) Pt { return Pt{o.F("x"), o.F("y")} }

// SetP writes {x,y} into an object.
func SetP(o *Obj, p Pt) { o.SetF("x", p.X); o.SetF("y", p.Y) }

// List returns one of the top-level arrays as objects (walls, stations, ...).
func (m *Map) List(key string) []*Obj { return Objs(m.Root.A(key)) }

// Walls returns the wall dots (normalPosList). The slice is cached; call ResetCache after edits.
func (m *Map) Walls() []Pt {
	if m.walls == nil {
		a := m.Root.A("normalPosList")
		m.walls = make([]Pt, 0, len(a))
		for _, v := range a {
			if o, ok := v.(*Obj); ok {
				m.walls = append(m.walls, P(o))
			}
		}
	}
	return m.walls
}

// ResetCache drops cached views after the tree was edited.
func (m *Map) ResetCache() { m.walls = nil }

// Station is an advanced point (LM, AP, PP, CP ...).
type Station struct {
	Name, Class string
	Pos         Pt
	Dir         float64
	HasDir      bool
	O           *Obj
}

func (m *Map) Stations() []Station {
	l := m.List("advancedPointList")
	out := make([]Station, 0, len(l))
	for _, o := range l {
		out = append(out, Station{Name: o.S("instanceName"), Class: o.S("className"), Pos: P(o.O("pos")), Dir: o.F("dir"), HasDir: o.IsNum("dir"), O: o})
	}
	return out
}

// Path is an advanced curve between two stations.
type Path struct {
	Name, From, To, Class string
	P0, P3                Pt
	C1, C2                *Pt
	O                     *Obj
}

func (m *Map) Paths() []Path {
	l := m.List("advancedCurveList")
	out := make([]Path, 0, len(l))
	// some maps give every path the same name (or none): such paths are named by their two stations
	count := map[string]int{}
	for _, o := range l {
		count[o.S("instanceName")]++
	}
	for _, o := range l {
		s, e := o.O("startPos"), o.O("endPos")
		name := o.S("instanceName")
		if name == "" || count[name] > 1 {
			name = s.S("instanceName") + "-" + e.S("instanceName")
		}
		p := Path{Name: name, Class: o.S("className"), From: s.S("instanceName"), To: e.S("instanceName"), P0: P(s.O("pos")), P3: P(e.O("pos")), O: o}
		if c := o.O("controlPos1"); c != nil {
			q := P(c)
			p.C1 = &q
		}
		if c := o.O("controlPos2"); c != nil {
			q := P(c)
			p.C2 = &q
		}
		out = append(out, p)
	}
	return out
}

// Sample returns points along the path every 2 % (a cubic Bezier when it has both control points).
func (p Path) Sample() []Pt {
	out := make([]Pt, 0, 51)
	for k := 0; k <= 50; k++ {
		t := float64(k) * 0.02
		if p.C1 != nil && p.C2 != nil {
			u := 1 - t
			out = append(out, Pt{
				u*u*u*p.P0.X + 3*u*u*t*p.C1.X + 3*u*t*t*p.C2.X + t*t*t*p.P3.X,
				u*u*u*p.P0.Y + 3*u*u*t*p.C1.Y + 3*u*t*t*p.C2.Y + t*t*t*p.P3.Y})
		} else {
			out = append(out, Pt{p.P0.X + t*(p.P3.X-p.P0.X), p.P0.Y + t*(p.P3.Y-p.P0.Y)})
		}
	}
	return out
}

// Len is the length of a polyline.
func Len(pts []Pt) float64 {
	l := 0.0
	for i := 1; i < len(pts); i++ {
		l += math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}
	return l
}

// Line is a feature line (advancedLineList).
type Line struct {
	Idx  int
	A, B Pt
	O    *Obj
}

func (m *Map) Lines() []Line {
	l := m.List("advancedLineList")
	out := make([]Line, 0, len(l))
	for i, o := range l {
		ln := o.O("line")
		out = append(out, Line{Idx: i, A: P(ln.O("startPos")), B: P(ln.O("endPos")), O: o})
	}
	return out
}

func (l Line) Mid() Pt { return Pt{(l.A.X + l.B.X) / 2, (l.A.Y + l.B.Y) / 2} }

// Bounds returns the bounding box of the wall dots.
func (m *Map) Bounds() (min, max Pt) {
	min, max = Pt{1e9, 1e9}, Pt{-1e9, -1e9}
	for _, p := range m.Walls() {
		min.X, min.Y = math.Min(min.X, p.X), math.Min(min.Y, p.Y)
		max.X, max.Y = math.Max(max.X, p.X), math.Max(max.Y, p.Y)
	}
	if len(m.Walls()) == 0 {
		return Pt{}, Pt{}
	}
	return
}

// NearGrid answers "how far is the nearest wall dot" quickly (0.5 m buckets, looks 1.5 m around).
type NearGrid map[[2]int][]Pt

func NewNearGrid(pts []Pt) NearGrid {
	g := NearGrid{}
	for _, p := range pts {
		k := [2]int{int(math.Floor(p.X / 0.5)), int(math.Floor(p.Y / 0.5))}
		g[k] = append(g[k], p)
	}
	return g
}

// Near returns the distance to the nearest dot, or 9 when nothing is within about 1.5 m.
func (g NearGrid) Near(p Pt) float64 {
	best := 9.0
	cx, cy := int(math.Floor(p.X/0.5)), int(math.Floor(p.Y/0.5))
	for i := -3; i <= 3; i++ {
		for j := -3; j <= 3; j++ {
			for _, q := range g[[2]int{cx + i, cy + j}] {
				if d := math.Hypot(q.X-p.X, q.Y-p.Y); d < best {
					best = d
				}
			}
		}
	}
	return best
}

// InPoly reports whether p is inside the polygon (even-odd rule).
func InPoly(p Pt, poly []Pt) bool {
	in := false
	n := len(poly)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < (b.X-a.X)*(p.Y-a.Y)/(b.Y-a.Y)+a.X {
			in = !in
		}
	}
	return in
}

// Rigid is a rotation by Th (radians) followed by a shift: x' = cos*x - sin*y + Tx.
type Rigid struct{ Th, Tx, Ty float64 }

func (t Rigid) Apply(p Pt) Pt {
	c, s := math.Cos(t.Th), math.Sin(t.Th)
	return Pt{c*p.X - s*p.Y + t.Tx, s*p.X + c*p.Y + t.Ty}
}

// Pivot is the point the transform leaves in place (ok=false for a pure shift).
func (t Rigid) Pivot() (Pt, bool) {
	c, s := math.Cos(t.Th), math.Sin(t.Th)
	a00, a01, a10, a11 := 1-c, s, -s, 1-c
	det := a00*a11 - a01*a10
	if math.Abs(det) < 1e-12 {
		return Pt{}, false
	}
	return Pt{(t.Tx*a11 - a01*t.Ty) / det, (a00*t.Ty - a10*t.Tx) / det}, true
}

// Then returns the transform "t first, then u".
func (t Rigid) Then(u Rigid) Rigid {
	o := u.Apply(Pt{t.Tx, t.Ty})
	return Rigid{t.Th + u.Th, o.X, o.Y}
}

// Inverse returns the transform that undoes t.
func (t Rigid) Inverse() Rigid {
	c, s := math.Cos(-t.Th), math.Sin(-t.Th)
	return Rigid{-t.Th, -(c*t.Tx - s*t.Ty), -(s*t.Tx + c*t.Ty)}
}

// R3 rounds to millimetres.
func R3(v float64) float64 { return JSRound(v*1000) / 1000 }

// JSRound rounds like JavaScript Math.round (halves go up), so results match the original scripts.
func JSRound(v float64) float64 { return math.Floor(v + 0.5) }
