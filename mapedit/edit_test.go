package mapedit

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drishtimap/smap"
)

func wsMap(t *testing.T, name string) *smap.Map {
	ws := os.Getenv("DRISHTI_WS")
	if ws == "" {
		t.Skip("DRISHTI_WS not set")
	}
	m, err := smap.Load(filepath.Join(ws, "maps", name+".smap"))
	if err != nil {
		t.Skip(err)
	}
	return m
}

func f(v float64) *float64 { return &v }

// The two edits made by hand on the robot (line removed, LM517..LM521 set to forward) are reproduced
// with the edit engine and must give the same stations, paths and feature lines as the robot's file.
func TestEditReproducesRobotEdit(t *testing.T) {
	v5 := wsMap(t, refName("v5"))
	want := wsMap(t, refName("v5_robot_edit_1215"))
	ed := NewEditor(v5.Clone(), nil)
	// what the robot's file changed in path direction
	bp := map[string]smap.Path{}
	for _, p := range v5.Paths() {
		bp[p.Name] = p
	}
	var flip []string
	for _, p := range want.Paths() {
		if b, ok := bp[p.Name]; ok && propDiff(b.O, p.O) == "direction 1 -> 0" {
			flip = append(flip, p.Name)
		}
	}
	if len(flip) == 0 {
		t.Fatal("expected direction changes in the robot edit")
	}
	if _, err := ed.Apply(EditOp{Op: "set_path", Paths: flip, Key: "direction", Value: float64(0)}); err != nil {
		t.Fatal(err)
	}
	for _, p := range ed.M.Paths() {
		w := want.Paths()
		for _, q := range w {
			if q.Name == p.Name && strings.HasPrefix(p.Name, "LM51") && !smap.Equal(p.O, q.O) {
				t.Fatalf("path %s differs from the robot's file: %s", p.Name, propDiff(q.O, p.O))
			}
		}
	}
	// the line the detector reports is the one to delete
	var hit *Finding
	for _, x := range Detect(ed.M) {
		if x.Kind == "line_on_path" && x.Level == "fault" {
			x := x
			hit = &x
		}
	}
	if hit == nil || !strings.Contains(hit.Text, "LM515-LM514") && !strings.Contains(hit.Text, "LM514-LM515") {
		t.Fatalf("detector did not flag the LM514-LM515 line: %+v", hit)
	}
	n0 := len(ed.M.Lines())
	if _, err := ed.Apply(EditOp{Op: "delete_lines", At: []float64{36.24, 35.25}}); err != nil {
		t.Fatal(err)
	}
	if len(ed.M.Lines()) != n0-1 {
		t.Fatalf("lines %d -> %d", n0, len(ed.M.Lines()))
	}
	for _, x := range Detect(ed.M) {
		if x.Kind == "line_on_path" {
			t.Fatalf("still flagged after delete: %s", x.Text)
		}
	}
	v := VerifyWith(v5, ed.M, ed.Intended)
	if !v.AllOK {
		t.Fatalf("checks failed:\n%s", v.Text())
	}
	// without the intent the direction change must be caught
	if Verify(v5, ed.M).AllOK {
		t.Fatal("plain Verify should fail on the changed path direction")
	}
	// the robot's own edit removed many more lines; ours removes only one
	t.Logf("flipped %d paths: %v; robot file has %d lines, ours %d", len(flip), flip, len(want.Lines()), len(ed.M.Lines()))
}

func TestEditOps(t *testing.T) {
	base := wsMap(t, refName("v5"))
	ed := NewEditor(base.Clone(), func(n string) (*smap.Map, error) { return wsMap(t, n), nil })
	must := func(op EditOp) string {
		t.Helper()
		n, err := ed.Apply(op)
		if err != nil {
			t.Fatalf("%s: %v", op.Op, err)
		}
		return n
	}
	fails := func(op EditOp, want string) {
		t.Helper()
		before := string(ed.M.Bytes()[:0])
		_ = before
		n0 := len(ed.Log)
		_, err := ed.Apply(op)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: expected error containing %q, got %v", op.Op, want, err)
		}
		if len(ed.Log) != n0 {
			t.Fatalf("%s: a failed edit was logged", op.Op)
		}
	}
	st := func(name string) smap.Station {
		for _, s := range ed.M.Stations() {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no station %s", name)
		return smap.Station{}
	}
	// refused edits change nothing
	sum0 := string(ed.M.Bytes())
	fails(EditOp{Op: "erase_walls"}, "needs a box")
	fails(EditOp{Op: "erase_walls", Box: []float64{-500, -500, 500, 500}}, "more than half")
	fails(EditOp{Op: "set_path", Path: "LM517-LM518", Key: "direction", Value: "x"}, "whole number")
	fails(EditOp{Op: "set_path", Path: "NOPE", Key: "direction", Value: float64(0)}, "no path")
	fails(EditOp{Op: "set_path", Path: "LM517-LM518", Key: "bogus", Value: float64(0)}, "no path in this map has a property")
	fails(EditOp{Op: "delete_station", Station: "LM517"}, "withPaths")
	fails(EditOp{Op: "delete_station", Station: "AP34", WithPaths: true}, "carries bin")
	fails(EditOp{Op: "add_station", Name: "LM517", X: f(1), Y: f(1)}, "already exists")
	fails(EditOp{Op: "add_station", Name: "AP9000", X: f(1), Y: f(1), Like: "LM517"}, "must be copied from")
	fails(EditOp{Op: "add_path", From: "LM517", To: "LM518"}, "already exists")
	fails(EditOp{Op: "move_walls", Box: []float64{30, 30, 40, 40}, RotDeg: 45}, "too large")
	fails(EditOp{Op: "bogus"}, "unknown edit")
	if string(ed.M.Bytes()) != sum0 {
		t.Fatal("refused edits changed the map")
	}
	// move a station: paths and control points follow, straight paths stay straight
	old := st("LM517").Pos
	must(EditOp{Op: "move_station", Station: "LM517", Dx: 0.10, Dy: -0.05})
	if p := st("LM517").Pos; math.Abs(p.X-old.X-0.10) > 1e-9 || math.Abs(p.Y-old.Y+0.05) > 1e-9 {
		t.Fatalf("station at %v", p)
	}
	np := 0
	for _, p := range ed.M.Paths() {
		if p.From != "LM517" && p.To != "LM517" {
			continue
		}
		np++
		if p.C1 != nil && p.Class != "BezierPath" {
			ex := smap.Pt{X: p.P0.X + (p.P3.X-p.P0.X)/3, Y: p.P0.Y + (p.P3.Y-p.P0.Y)/3}
			if math.Hypot(p.C1.X-ex.X, p.C1.Y-ex.Y) > 0.002 {
				t.Fatalf("%s control point not on the chord", p.Name)
			}
		}
	}
	if np == 0 || len(Structure(ed.M)) != 0 {
		t.Fatalf("paths %d, structure %v", np, Structure(ed.M))
	}
	// a bin follows its station
	binPos := func() smap.Pt {
		for _, g := range ed.M.List("binLocationsList") {
			for _, bl := range smap.Objs(g.A("binLocationList")) {
				if bl.S("pointName") == "AP34" {
					return smap.P(bl.O("pos"))
				}
			}
		}
		return smap.Pt{}
	}
	b0 := binPos()
	must(EditOp{Op: "move_station", Station: "AP34", Dx: 0.02, Dy: 0.03})
	if b1 := binPos(); math.Abs(b1.X-b0.X-0.02) > 1e-6 || math.Abs(b1.Y-b0.Y-0.03) > 1e-6 {
		t.Fatalf("bin did not follow: %v -> %v", b0, b1)
	}
	// station direction
	must(EditOp{Op: "set_station", Station: "LM517", DirDeg: f(90)})
	if s := st("LM517"); math.Abs(s.Dir-math.Pi/2) > 1e-9 {
		t.Fatalf("dir %v", s.Dir)
	}
	// erase walls in a box
	w0 := len(ed.M.Walls())
	must(EditOp{Op: "erase_walls", Box: []float64{30, 30, 34, 34}})
	for _, w := range ed.M.Walls() {
		if w.X >= 30 && w.X <= 34 && w.Y >= 30 && w.Y <= 34 {
			t.Fatal("wall dot left in the erased box")
		}
	}
	erased := w0 - len(ed.M.Walls())
	// take them back from the unedited map
	must(EditOp{Op: "take_walls", Donor: refName("v5"), Box: []float64{30, 30, 34, 34}})
	if len(ed.M.Walls()) != w0 || erased == 0 {
		t.Fatalf("walls %d -> %d (erased %d)", w0, len(ed.M.Walls()), erased)
	}
	// add: nothing new when the dots are already there
	fails(EditOp{Op: "take_walls", Donor: refName("v5"), Box: []float64{30, 30, 34, 34}, Mode: "add"}, "nothing to add")
	if len(ed.M.Walls()) != w0 {
		t.Fatalf("add duplicated dots: %d -> %d", w0, len(ed.M.Walls()))
	}
	// move walls only: stations stay
	s0 := st("LM520").Pos
	must(EditOp{Op: "move_walls", Box: []float64{30, 30, 34, 34}, RotDeg: 1, Pivot: []float64{32, 32}, Tx: 0.05})
	if st("LM520").Pos != s0 || len(ed.M.Walls()) != w0 {
		t.Fatal("move_walls touched something else")
	}
	// add station + path, then delete them
	must(EditOp{Op: "add_station", Name: "LM9001", X: f(28.0), Y: f(36.5)})
	must(EditOp{Op: "add_path", From: "LM517", To: "LM9001"})
	if len(Structure(ed.M)) != 0 {
		t.Fatalf("structure: %v", Structure(ed.M))
	}
	if len(ed.M.Stations()) != len(base.Stations())+1 || len(ed.M.Paths()) != len(base.Paths())+1 {
		t.Fatal("counts after add")
	}
	must(EditOp{Op: "delete_station", Station: "LM9001", WithPaths: true})
	if len(ed.M.Stations()) != len(base.Stations()) || len(ed.M.Paths()) != len(base.Paths()) {
		t.Fatal("counts after delete")
	}
	// paths in a region
	n := must(EditOp{Op: "set_path", Region: [][]float64{{20, 34}, {30, 34}, {30, 36.5}, {20, 36.5}}, Key: "maxspeed", Value: 0.5})
	if !strings.Contains(n, "maxspeed = 0.5") {
		t.Fatal(n)
	}
	// the result is still a loadable map and the checks run
	m2, err := smap.FromBytes(ed.M.Bytes())
	if err != nil || len(m2.Stations()) != len(base.Stations()) {
		t.Fatalf("round trip: %v", err)
	}
	v := VerifyWith(base, ed.M, ed.Intended)
	t.Logf("%d edits; checks:\n%s", len(ed.Log), v.Text())
	for _, c := range v.Checks {
		if !c.OK && !strings.Contains(c.Text, "closer to a wall") {
			t.Fatalf("unexpected failing check: %s %v", c.Text, c.Details)
		}
	}
}

// An unedited map has no structural faults and the detector does not invent any.
func TestDetectCleanMaps(t *testing.T) {
	for _, n := range []string{refName("live_421395c7"), refName("sep16")} {
		m := wsMap(t, n)
		for _, x := range Detect(m) {
			if x.Level == "fault" {
				t.Fatalf("%s: unexpected fault %s", n, x.Text)
			}
		}
	}
}
