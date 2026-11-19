package mapedit

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"drishtimap/smap"
)

// These tests replay the reference v5 build and must give the same numbers as the original
// scripts that produced the map trialled on AMR-03. They need the reference files:
//
//	DRISHTI_WS  = workspace folder (maps\...)
//	DRISHTI_REF = folder with T.json, moved.smap and final.smap written by the original scripts
func refs(t *testing.T) (ws, ref string) {
	ws, ref = os.Getenv("DRISHTI_WS"), os.Getenv("DRISHTI_REF")
	if ws == "" || ref == "" {
		t.Skip("DRISHTI_WS / DRISHTI_REF not set")
	}
	return
}

func load(t *testing.T, p string) *smap.Map {
	m, err := smap.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// diffTrees counts leaf values that differ between two trees (numbers compared as float64).
func diffTrees(a, b any, path string, out *[]string) {
	switch x := a.(type) {
	case *smap.Obj:
		y, ok := b.(*smap.Obj)
		if !ok || len(x.Keys) != len(y.Keys) {
			*out = append(*out, path+": object shape")
			return
		}
		for i, k := range x.Keys {
			if y.Keys[i] != k {
				*out = append(*out, path+": key order "+k+" vs "+y.Keys[i])
				return
			}
			diffTrees(x.Vals[i], y.Vals[i], path+"."+k, out)
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: array length %d vs %d", path, len(x), len(y)))
			return
		}
		for i := range x {
			diffTrees(x[i], y[i], fmt.Sprintf("%s[%d]", path, i), out)
		}
	case smap.Num:
		y, ok := b.(smap.Num)
		if !ok {
			*out = append(*out, path+": type")
			return
		}
		var fa, fb float64
		fmt.Sscan(string(x), &fa)
		fmt.Sscan(string(y), &fb)
		if fa != fb {
			*out = append(*out, fmt.Sprintf("%s: %s vs %s", path, x, y))
		}
	default:
		if a != b {
			*out = append(*out, fmt.Sprintf("%s: %v vs %v", path, a, b))
		}
	}
}

func TestRoundTrip(t *testing.T) {
	ws, _ := refs(t)
	for _, f := range []string{"SPRAMRMAP_live_421395c7.smap", "SPRAMRMAP_v5_robot_edit_1215.smap"} {
		raw, _ := os.ReadFile(filepath.Join(ws, "maps", f))
		m, err := smap.FromBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		out := m.Bytes()
		if string(out) != string(raw) {
			// not byte-identical (e.g. the source had spaces): the content must still be equal
			m2, err := smap.FromBytes(out)
			if err != nil {
				t.Fatal(err)
			}
			var d []string
			diffTrees(m.Root, m2.Root, "", &d)
			if len(d) > 0 {
				t.Fatalf("%s: round trip changed content: %v", f, d[:1])
			}
			t.Logf("%s: round trip keeps every value; bytes differ only in spacing (%d -> %d)", f, len(raw), len(out))
		} else {
			t.Logf("%s: round trip is byte-identical (%d bytes)", f, len(raw))
		}
	}
}

// the region the v5 build used: y -14..42, x >= 7.5 (south leg) / 12 (middle) / 15 (north)
var sprRegion = pts(7.5, -14, 7.5, 1, 12, 3, 12, 29, 15, 31, 15, 42, 500, 42, 500, -14)

func pts(v ...float64) (o []smap.Pt) {
	for i := 0; i+1 < len(v); i += 2 {
		o = append(o, smap.Pt{X: v[i], Y: v[i+1]})
	}
	return
}

func TestV5Rebuild(t *testing.T) {
	ws, ref := refs(t)
	var T struct{ Th, Tx, Ty float64 }
	b, err := os.ReadFile(filepath.Join(ref, "T.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &T)
	base := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_live_421395c7.smap"))
	anchor := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_sep16.smap"))
	donor := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_v3_orig.smap"))
	cand := base.Clone()
	rep, err := MoveRegion(cand, MoveParams{T: smap.Rigid{Th: T.Th, Tx: T.Tx, Ty: T.Ty}, Region: sprRegion, Anchor: anchor, MapName: "SPRAMRMAP_v5"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("moved: walls %d (anchored %d), rssi %d, reflectors %d, stations %d, lines %d (kept %d), areas %v, bins %d; pivot %v",
		rep.WallsMoved, rep.WallsAnchored, rep.RssiMoved, rep.ReflectorsMoved, rep.StationsMoved, rep.LinesMoved, rep.LinesKept, rep.AreasMoved, rep.BinsMoved, *rep.Pivot)
	if rep.WallsMoved != 65789 || rep.WallsAnchored != 4224 || rep.RssiMoved != 949 || rep.ReflectorsMoved != 10 || rep.StationsMoved != 37 {
		t.Errorf("counts differ from the original build (65789 / 4224 / 949 / 10 / 37)")
	}
	var d []string
	diffTrees(load(t, filepath.Join(ref, "moved.smap")).Root, cand.Root, "", &d)
	if len(d) > 0 {
		t.Errorf("move step: %d values differ from the original script, first: %v", len(d), d[:min(5, len(d))])
	}
	br := MergeBox(cand, donor, 9, 31, 29.5, 42)
	t.Logf("box: %+v", *br)
	d = nil
	diffTrees(load(t, filepath.Join(ref, "final.smap")).Root, cand.Root, "", &d)
	if len(d) > 0 {
		t.Errorf("box step: %d values differ from the original script, first: %v", len(d), d[:min(5, len(d))])
	}
	// written file parses again and passes the checks
	cand2, err := smap.FromBytes(cand.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(base, cand2)
	t.Log("\n" + v.Text())
	if !v.AllOK {
		t.Errorf("rebuilt candidate fails its checks")
	}
}

// The file that was actually loaded on AMR-03 had one feature line left on path LM514-LM515.
// The check must catch it.
func TestStaleLineIsCaught(t *testing.T) {
	ws, _ := refs(t)
	base := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_live_421395c7.smap"))
	v5 := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_v5.smap"))
	v := Verify(base, v5)
	found := false
	for _, n := range v.NearLines {
		t.Logf("near line: %s base %.2f cand %.2f line #%d (%.2f,%.2f)-(%.2f,%.2f)", n.Path, n.Base, n.Cand, n.LineIdx, n.A.X, n.A.Y, n.B.X, n.B.Y)
		if (n.Path == "LM514-LM515" || n.Path == "LM515-LM514") && math.Abs(n.A.X-36.16) < 0.02 {
			found = true
		}
	}
	if !found || v.AllOK {
		t.Errorf("the stale feature line at LM514-LM515 was not reported")
	}
}

func TestCompareRobotEdit(t *testing.T) {
	ws, _ := refs(t)
	a := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_v5.smap"))
	b := load(t, filepath.Join(ws, "maps", "SPRAMRMAP_v5_robot_edit_1215.smap"))
	d := Compare(a, b)
	t.Log("\n" + d.Text("v5", "robot edit"))
	if d.LinesOnlyA != 144 || d.WallsOnlyA != 217 || len(d.PathsChanged) != 6 || len(d.StationsChanged) != 0 {
		t.Errorf("expected 144 removed feature lines and changed paths, got %d lines, %d paths", d.LinesOnlyA, len(d.PathsChanged))
	}
}
