package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkPass(robot string, min float64, at time.Time, dy float64) Pass {
	return Pass{T0: at, T1: at.Add(20 * time.Second), Robot: robot, Seconds: 20, MinConf: min, AvgConf: min + 0.1, DY: dy}
}

// The verdict must follow the counts: these are built cases with a known answer.
func TestAreaVerdicts(t *testing.T) {
	t0 := time.Date(2026, 1, 10, 8, 0, 0, 0, time.Local)
	mk := func(f func(add func(robot string, min float64))) *Area {
		a := &Area{Global: map[string]float64{}, Codes: map[string]string{}, From: t0, To: t0.Add(24 * time.Hour)}
		i := 0
		f(func(robot string, min float64) {
			i++
			a.Passes = append(a.Passes, mkPass(robot, min, t0.Add(time.Duration(i)*7*time.Minute), float64(1-2*(i%2))*5))
		})
		return a
	}
	robots := []string{"R1", "R2", "R3", "R4", "R5", "R6"}
	cases := []struct {
		name, kind string
		isMap      bool
		a          *Area
	}{
		{"every robot low", "place", true, mk(func(add func(string, float64)) {
			for k := 0; k < 6; k++ {
				for _, r := range robots {
					add(r, 0.35)
				}
			}
		})},
		{"one robot only", "robot", false, mk(func(add func(string, float64)) {
			for k := 0; k < 5; k++ {
				for _, r := range robots[1:] {
					add(r, 0.8)
				}
				add("R1", 0.2)
			}
		})},
		{"all fine", "nothing", false, mk(func(add func(string, float64)) {
			for k := 0; k < 4; k++ {
				for _, r := range robots {
					add(r, 0.8)
				}
			}
		})},
		{"two passes", "nodata", false, mk(func(add func(string, float64)) { add("R1", 0.2); add("R2", 0.2) })},
		{"most robots had one bad pass", "marginal", true, mk(func(add func(string, float64)) {
			for k := 0; k < 6; k++ {
				for _, r := range robots {
					if k == 0 && r != "R6" {
						add(r, 0.2)
					} else {
						add(r, 0.7)
					}
				}
			}
		})},
	}
	for _, c := range cases {
		r := AnalyseArea(c.a, "")
		if r.Kind != c.kind || r.IsMap != c.isMap {
			t.Errorf("%s: verdict %q (%s), map problem %v; want %s, %v", c.name, r.Verdict, r.Kind, r.IsMap, c.kind, c.isMap)
		}
	}
	// the same robot low everywhere in the plant points at its scanner, not at the place
	a := cases[1].a
	for _, r := range robots {
		a.Global[r] = 0.8
	}
	a.Global["R1"] = 0.45
	if r := AnalyseArea(a, ""); r.Kind != "scanner" {
		t.Errorf("robot low everywhere: verdict %q (%s), want scanner", r.Verdict, r.Kind)
	}
	// confidence drops after a map push inside the time range
	b := &Area{Global: map[string]float64{}, Codes: map[string]string{}, From: t0, To: t0.Add(24 * time.Hour), Pushes: []time.Time{t0.Add(12 * time.Hour)}}
	for k := 0; k < 12; k++ {
		for _, r := range robots {
			m := 0.8
			if k >= 6 {
				m = 0.4
			}
			b.Passes = append(b.Passes, mkPass(r, m, t0.Add(time.Duration(k)*2*time.Hour+time.Minute), 5))
		}
	}
	if r := AnalyseArea(b, ""); r.Kind != "mapchange" {
		t.Errorf("drop after a map push: verdict %q (%s), want mapchange", r.Verdict, r.Kind)
	}
}

// Real lane, two days (set DRISHTI_WS): 11 robots, about 119 passes; marginal place, one stop with errors.
func TestRealArea(t *testing.T) {
	ws := os.Getenv("DRISHTI_WS")
	if ws == "" {
		t.Skip("DRISHTI_WS not set")
	}
	b, err := os.ReadFile(filepath.Join(ws, "history", "area_lane_20261118_20.tsv"))
	if err != nil {
		t.Skip("no captured area in the workspace")
	}
	a := ParseArea(strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n"))
	a.Box = [4]float64{1.7, 54.5, 5.7, 63.5}
	a.From, a.To = pt("2026-11-18 07:00:00"), pt("2026-11-20 07:00:00")
	r := AnalyseArea(a, "AMR-08")
	t.Log("\n" + r.Text(a))
	if len(r.Robots) != 11 || r.Passes < 100 {
		t.Fatalf("robots %d, passes %d", len(r.Robots), r.Passes)
	}
	for _, w := range r.Robots {
		if w.Robot == "AMR-08" && (w.Worst > 0.15 || w.Stops != 1 || len(w.Errors) != 2) {
			t.Errorf("AMR-08: lowest %.2f, stops %d, errors %v; want 0.1, 1 stop, blocked and slipping", w.Worst, w.Stops, w.Errors)
		}
		if w.LowAll {
			t.Errorf("%s flagged low everywhere (%.2f): no robot is", w.Robot, w.Elsewhere)
		}
	}
	// the trouble is the place (worse since 19 Nov), almost only heading north
	if !r.IsMap || (r.Kind != "mapchange" && r.Kind != "marginal" && r.Kind != "place") || r.Rating == "LOW" {
		t.Errorf("verdict %q (%s) rating %s; want the place, not LOW", r.Verdict, r.Kind, r.Rating)
	}
	if !strings.Contains(strings.Join(r.Patterns, "|"), "heading north") {
		t.Errorf("the one-direction pattern was not found: %v", r.Patterns)
	}
	if len(a.Pushes) == 0 || len(a.Points) < 500 {
		t.Errorf("map pushes %d, drawn points %d", len(a.Pushes), len(a.Points))
	}
}
