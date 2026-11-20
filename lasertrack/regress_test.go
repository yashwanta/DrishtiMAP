package lasertrack

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"drishtimap/smap"
)

// Reference results come from the original analysis of the reference AMR-06 loop:
//
//	DRISHTI_WS   workspace (maps\, drives\)
//	DRISHTI_OUT  folder with d6_poses.txt, replay_cur1113.txt, replay_v3_orig.txt, final_tracking.txt, T.json values
func env(t *testing.T) (ws, out string) {
	ws, out = os.Getenv("DRISHTI_WS"), os.Getenv("DRISHTI_OUT")
	if ws == "" || out == "" {
		t.Skip("DRISHTI_WS / DRISHTI_OUT not set")
	}
	return
}

func table(t *testing.T, f string) [][]string {
	fh, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var rows [][]string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if l := sc.Text(); l != "" && l[0] != '#' {
			rows = append(rows, strings.Fields(strings.ReplaceAll(l, "|", " ")))
		}
	}
	return rows
}
func num(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

var chain6 []ChainPose
var samples6 []*Sample

func drive6(t *testing.T) ([]*Sample, []ChainPose) {
	ws, _ := env(t)
	if chain6 == nil {
		var err error
		samples6, err = LoadSamples(filepath.Join(ws, "drives", "amr06_loop_nov13"), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		chain6 = Chain(samples6, ChainOpts{})
	}
	return samples6, chain6
}

func TestChainMatchesOriginal(t *testing.T) {
	_, out := env(t)
	_, ch := drive6(t)
	ref := table(t, filepath.Join(out, "d6_poses.txt"))
	if len(ref) != len(ch) {
		t.Fatalf("samples: %d vs original %d", len(ch), len(ref))
	}
	// up to scan 370 (before the robot was relocalised at LM607) the track must equal the original analysis
	worst, off := 0.0, 0
	for k, r := range ref[:370] {
		d := math.Hypot(ch[k].X-num(r[5]), ch[k].Y-num(r[6]))
		worst = math.Max(worst, d)
		if d > 0.30 { // the track drifts a little differently with the centred grid; shape must agree within 0.3 m
			off++
		}
	}
	t.Logf("laser track: %d scans, %.1f m; largest difference to the original track (scans 1-370) %.3f m", len(ch), TrackLength(ch), worst)
	if off > 0 {
		t.Errorf("laser track differs from the original on %d scans", off)
	}
}

func hasJump(r *Replay, min float64, names ...string) bool {
	for _, j := range r.Jumps {
		for _, n := range names {
			if j.Near == n && j.Step >= min {
				return true
			}
		}
	}
	return false
}

// What happened on the floor is the reference: the live map jumps between LM520 and LM459, v3 and v4c
// lose the robot around LM441-LM443, v5 drove the whole loop on AMR-03 without a jump.
// The verdict must be the same wherever the internal grid happens to sit.
func TestReplayMatchesFloor(t *testing.T) {
	ws, _ := env(t)
	ss, _ := drive6(t)
	load := func(n string) *smap.Map {
		m, err := smap.Load(filepath.Join(ws, "maps", n))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	live, v3, v4c, v5 := load(refName("live_421395c7.smap")), load(refName("v3_orig.smap")), load(refName("v4c.smap")), load(refName("v5.smap"))
	defer func() { GridShift = 0 }()
	var turns []float64
	for _, sh := range []float64{0, 0.013, 0.027} {
		GridShift = sh
		ch := Chain(ss, ChainOpts{})
		rl, err := Run(ss, ch, live, ReplayOpts{})
		if err != nil {
			t.Fatal(err)
		}
		r3, _ := Run(ss, ch, v3, ReplayOpts{})
		r4, _ := Run(ss, ch, v4c, ReplayOpts{})
		r5, _ := Run(ss, ch, v5, ReplayOpts{})
		if sh == 0 {
			t.Log("\n" + rl.Text("live map") + "\n" + r5.Text("v5"))
		}
		if !hasJump(rl, 1.0, "LM519", "LM520", "LM521", "LM459") {
			t.Errorf("grid %.3f: live map must show the jump at LM520-LM459", sh)
		}
		if !hasJump(r3, 1.0, "LM440", "LM441", "LM442", "LM443") || !hasJump(r4, 1.0, "LM441", "LM442", "LM443") {
			t.Errorf("grid %.3f: v3 and v4c must show the step at LM441-LM443", sh)
		}
		if len(r5.Jumps) != 0 {
			t.Errorf("grid %.3f: v5 must show no jump, got %d", sh, len(r5.Jumps))
		}
		// the turn: piece = live east loop (scans 40-262), reference = v3 corridor/west (scans 172-372)
		aL, err := AlignTrack(ch, rl.Rows, []Range{{40, 262}}, 45)
		if err != nil {
			t.Fatal(err)
		}
		aV, err := AlignTrack(ch, r3.Rows, []Range{{172, 372}}, 45)
		if err != nil {
			t.Fatal(err)
		}
		tr := MeasureTurn(aL, aV)
		t.Logf("grid %.3f: live jumps %d, v3 %d, v4c %d, v5 %d; turn %.3f deg tx %.3f ty %.3f (piece rms %.3f, ref rms %.3f)", sh, len(rl.Jumps), len(r3.Jumps), len(r4.Jumps), len(r5.Jumps), tr.RotDeg, tr.T.Tx, tr.T.Ty, aL.RMS, aV.RMS)
		turns = append(turns, tr.RotDeg)
		if math.Abs(tr.RotDeg-2.187) > 0.3 {
			t.Errorf("turn differs from the 2.187 deg used for v5 by more than 0.3 deg")
		}
		if sh == 0 {
			t.Log("\n" + tr.Text() + "\n" + PiecesText(Pieces(ch, rl.Rows, 45, 0)))
			dr, al, _ := Drift(ch, rl.Rows, []Range{{40, 262}}, 45)
			t.Log("\n" + DriftText(dr, al, 24))
			// one rigid track on v5: every scan placed by the laser track, no following
			al5, _ := AlignTrack(ch, r5.Rows, []Range{{1, 370}}, 45)
			fx, _ := Run(ss, ch, v5, ReplayOpts{Fixed: &al5.T})
			t.Logf("v5 as one rigid track (scans 1-370): fit rms %.3f m, worst %.2f m; fixed replay average match %.0f%%", al5.RMS, al5.Max, fx.AvgFit)
			if al5.RMS > 0.15 {
				t.Errorf("v5 should fit the laser track as one rigid piece (rms %.3f)", al5.RMS)
			}
		}
	}
	if m := mean(turns); math.Abs(m-2.187) > 0.15 {
		t.Errorf("mean turn %.3f deg is not within 0.15 deg of 2.187", m)
	} else {
		t.Logf("mean turn over 3 grid placements %.3f deg (v5 used 2.187)", m)
	}
}

func TestSkipStillGivesSameTrack(t *testing.T) {
	ss, ch := drive6(t)
	ch2 := Chain(ss, ChainOpts{SkipStill: true})
	worst, still := 0.0, 0
	for k := range ch {
		worst = math.Max(worst, math.Hypot(ch[k].X-ch2[k].X, ch[k].Y-ch2[k].Y))
		if k > 0 && ch2[k].X == ch2[k-1].X && ch2[k].Y == ch2[k-1].Y {
			still++
		}
	}
	t.Logf("skip-still: %d scans not re-matched, largest difference to the full track %.3f m", still, worst)
	if worst > 0.25 {
		t.Errorf("skip-still changes the track by %.2f m", worst)
	}
}

func TestSummary(t *testing.T) {
	ws, _ := env(t)
	s, err := Summarize(filepath.Join(ws, "drives", "amr03_v5_trial_nov17"), 1, 1800)
	if err != nil {
		t.Fatal(err)
	}
	txt := s.Text()
	if len(txt) > 3500 {
		txt = txt[:3500]
	}
	t.Log("\n" + txt)
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// refName returns the name of a reference map in the test workspace. The maps carry the site's own
// map name as a prefix; it is read from the files so that it does not have to be written here.
func refName(suffix string) string {
	m, _ := filepath.Glob(filepath.Join(os.Getenv("DRISHTI_WS"), "maps", "*_live_421395c7.smap"))
	if len(m) == 0 {
		return "MAP_" + suffix
	}
	return strings.TrimSuffix(filepath.Base(m[0]), "live_421395c7.smap") + suffix
}
