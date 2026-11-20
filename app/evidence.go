package app

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"drishtimap/lasertrack"
	"drishtimap/smap"
)

// boxEvidence lays the lasers of one or more recordings over maps, inside a box, and says what
// matches. The recordings are placed with the replay through the first map.
//
// It answers: how well does each map version fit what the robots really see here; which walls of
// the first map were seen; and what the lasers see that the map does not have, split into "in every
// recording" (something permanent) and "in some recordings only" (something passing through).
// A wall that was not seen is not proof that it is gone: it can be hidden behind something.
type mapFit struct {
	Map   string  `json:"map"`
	Match float64 `json:"match"` // % of laser points on a wall of this map, for scans taken inside the box (each map places the scans itself)
	Walls int     `json:"walls"`
}

const evTol = 0.15 // metres: a laser point this close to a wall dot counts as that wall

func cellOf(p smap.Pt) [2]int { return [2]int{int(math.Floor(p.X / 0.1)), int(math.Floor(p.Y / 0.1))} }
func cellPt(c [2]int) smap.Pt {
	return smap.Pt{X: (float64(c[0]) + 0.5) * 0.1, Y: (float64(c[1]) + 0.5) * 0.1}
}
func inBox(p smap.Pt, b [4]float64) bool {
	return p.X >= b[0] && p.X <= b[2] && p.Y >= b[1] && p.Y <= b[3]
}

func (a *App) boxEvidence(c *Ctx, box [4]float64, drives, maps []string) (*Result, error) {
	if len(drives) == 0 || len(maps) == 0 {
		return nil, fmt.Errorf("give at least one recording and one map")
	}
	if len(drives) > 8 || len(maps) > 6 {
		return nil, fmt.Errorf("at most 8 recordings and 6 maps at a time")
	}
	// lasers of each recording, in the first map's frame, as 10 cm cells inside the box
	per := make([]map[[2]int]bool, len(drives))
	union := map[[2]int]int{} // cell -> in how many recordings
	passes := 0
	for i, d := range drives {
		ss, _, _, rep, err := a.follow(c, d, maps[0], 0, 0)
		if err != nil {
			return nil, fmt.Errorf("recording %s: %v", d, err)
		}
		byI := map[int][]smap.Pt{}
		for _, s := range ss {
			byI[s.I] = s.RP
		}
		hits := map[[2]int]int{}
		inside := false
		for _, row := range rep.Rows {
			here := inBox(smap.Pt{X: row.X, Y: row.Y}, box)
			if here && !inside {
				passes++
			}
			inside = here
			cs, sn := math.Cos(row.Deg*math.Pi/180), math.Sin(row.Deg*math.Pi/180)
			once := map[[2]int]bool{}
			for _, p := range byI[row.I] {
				if p.X*p.X+p.Y*p.Y > 12*12 {
					continue // far points are too coarse to judge a wall by
				}
				q := smap.Pt{X: row.X + cs*p.X - sn*p.Y, Y: row.Y + sn*p.X + cs*p.Y}
				if k := cellOf(q); inBox(q, box) && !once[k] {
					once[k] = true
					hits[k]++
				}
			}
		}
		cells := map[[2]int]bool{}
		for k, n := range hits {
			if n >= 3 { // seen by three scans or more: a real surface, not a stray point or someone walking by
				cells[k] = true
			}
		}
		per[i] = cells
		for k := range cells {
			union[k]++
		}
		if c.Err() != nil {
			return nil, c.Err()
		}
	}
	if len(union) < 25 {
		return nil, fmt.Errorf("the recording(s) show almost nothing inside the box (%d laser cells). Choose a recording that drives through it, or draw the box around the walls beside the path", len(union))
	}
	var las []smap.Pt
	for k := range union {
		las = append(las, cellPt(k))
	}
	lasGrid := smap.NewNearGrid(las)

	// each map version: how much of what the lasers see lies on its walls
	var fits []mapFit
	var first *smap.Map
	var firstWalls []smap.Pt
	for i, name := range maps {
		m, err := a.Map(name)
		if err != nil {
			return nil, err
		}
		var w []smap.Pt
		for _, p := range m.Walls() {
			if p.X >= box[0]-evTol && p.X <= box[2]+evTol && p.Y >= box[1]-evTol && p.Y <= box[3]+evTol {
				w = append(w, p)
			}
		}
		sum, n := 0.0, 0
		for _, d := range drives {
			_, _, _, rep, err := a.follow(c, d, name, 0, 0)
			if err != nil {
				return nil, fmt.Errorf("recording %s on %s: %v", d, name, err)
			}
			for _, row := range rep.Rows {
				if row.X >= box[0]-1 && row.X <= box[2]+1 && row.Y >= box[1]-1 && row.Y <= box[3]+1 {
					sum += row.Fit
					n++
				}
			}
			if c.Err() != nil {
				return nil, c.Err()
			}
		}
		if n == 0 {
			return nil, fmt.Errorf("no recording drives through the box on %s; draw the box over the path the robot drove", name)
		}
		fits = append(fits, mapFit{Map: name, Match: sum / float64(n), Walls: len(w)})
		if i == 0 {
			first, firstWalls = m, w
		}
	}
	_ = first

	// the first map in detail
	wg := smap.NewNearGrid(firstWalls)
	var seen, unseen, always, sometimes []smap.Pt
	for _, p := range firstWalls {
		if !inBox(p, box) {
			continue
		}
		if lasGrid.Near(p) <= evTol {
			seen = append(seen, p)
		} else {
			unseen = append(unseen, p)
		}
	}
	for k, n := range union {
		p := cellPt(k)
		if wg.Near(p) <= evTol {
			continue
		}
		if n == len(drives) {
			always = append(always, p)
		} else {
			sometimes = append(sometimes, p)
		}
	}
	nw := len(seen) + len(unseen)
	pct := func(n, of int) float64 {
		if of == 0 {
			return 0
		}
		return 100 * float64(n) / float64(of)
	}
	extra := len(always) + len(sometimes)

	var b strings.Builder
	fmt.Fprintf(&b, "LASER EVIDENCE in the box  x %.1f..%.1f  y %.1f..%.1f\n", box[0], box[2], box[1], box[3])
	fmt.Fprintf(&b, "%d recording(s) (%s), %d pass(es) through the box, placed on %s\n\n", len(drives), strings.Join(drives, ", "), passes, maps[0])
	b.WriteString("HOW WELL EACH MAP FITS WHAT THE LASERS SEE HERE\n")
	best := 0
	for i, f := range fits {
		if f.Match > fits[best].Match {
			best = i
		}
	}
	for i, f := range fits {
		note := ""
		if i == 0 {
			note = "  (the map in question)"
		}
		if i == best && len(fits) > 1 {
			note += "  <-- best fit"
		}
		fmt.Fprintf(&b, "  %-40s %5.1f %%%s\n", f.Map, f.Match, note)
	}
	fmt.Fprintf(&b, "\nTHE MAP IN QUESTION (%s), %d wall dots in the box\n", maps[0], nw)
	fmt.Fprintf(&b, "  seen by the lasers                      %5.1f %%\n", pct(len(seen), nw))
	fmt.Fprintf(&b, "  not seen in these recordings            %5.1f %%   (gone, or hidden behind something)\n", pct(len(unseen), nw))
	fmt.Fprintf(&b, "\nWHAT THE LASERS SEE THAT IS NOT IN THE MAP, %d cells of 10 cm (%.0f %% of all laser cells)\n", extra, pct(extra, len(las)))
	if len(drives) > 1 {
		fmt.Fprintf(&b, "  in every recording                      %5d   something permanent that the map does not have\n", len(always))
		fmt.Fprintf(&b, "  in some recordings only                 %5d   something passing through or put down for a while\n", len(sometimes))
	} else {
		fmt.Fprintf(&b, "  %d cells. With one recording the program cannot tell a permanent object from a passing one; add a second recording made at another time\n", extra)
	}
	// reading
	b.WriteString("\nREADING\n")
	cur := fits[0].Match
	switch {
	case len(fits) > 1 && best != 0 && fits[best].Match >= cur+5:
		fmt.Fprintf(&b, "  %s fits the lasers better here than %s (%.0f %% against %.0f %%): its walls in this box are a candidate fix.\n", fits[best].Map, maps[0], fits[best].Match, cur)
	case cur >= 55:
		fmt.Fprintf(&b, "  %s fits the lasers %.0f %% here, which is what a good map gives (50 to 75 %%), and no other version fits clearly better. The walls in this box are probably not the cause.\n", maps[0], cur)
	case len(fits) > 1:
		fmt.Fprintf(&b, "  No other version fits clearly better (best %.0f %%, the map in question %.0f %%). The floor has changed since every scan: this area needs a rescan.\n", fits[best].Match, cur)
	default:
		fmt.Fprintf(&b, "  The map fits %.0f %% here. Add older map versions to see whether one of them fits better.\n", cur)
	}
	if len(drives) > 1 && len(sometimes) > 3*len(always) && len(sometimes) > 30 {
		b.WriteString("  Most of what is not in the map comes and goes: things are put down and taken away here. No map can be right for that.\n")
	}
	sort.SliceStable(fits[1:], func(i, j int) bool { return fits[1:][i].Match > fits[1:][j].Match })
	return &Result{Summary: b.String(), Data: map[string]any{
		"fits": fits, "best": fits[0].Map, "bestOther": bestOther(fits, cur), "box": box, "passes": passes, "drives": len(drives),
		"seenPct": pct(len(seen), nw), "unseenPct": pct(len(unseen), nw), "extraPct": pct(extra, len(las)),
		"always": len(always), "sometimes": len(sometimes),
		"ptsSeen": f32(seen), "ptsUnseen": f32(unseen), "ptsAlways": f32(always), "ptsSometimes": f32(sometimes),
	}}, nil
}

// bestOther names the other map version that fits at least 5 points better than the first, if any.
func bestOther(fits []mapFit, cur float64) string {
	if len(fits) > 1 && fits[1].Match >= cur+5 {
		return fits[1].Map
	}
	return ""
}

// trialResult judges a trial recording inside the box: enough samples, no position jump, confidence kept.
func (a *App) trialResult(drive string, box [4]float64, before float64) (*Result, error) {
	dir, err := a.drivePath(drive)
	if err != nil {
		return nil, err
	}
	sm, err := lasertrack.Summarize(dir, 0, 0)
	if err != nil {
		return nil, err
	}
	n, jumps, passes, refl := 0, 0, 0, 0
	minC, sum := 2.0, 0.0
	inside := false
	for i, s := range sm.Track {
		here := inBox(smap.Pt{X: s.X, Y: s.Y}, box)
		if here && !inside {
			passes++
		}
		inside = here
		if !here {
			continue
		}
		if i > 0 && math.Hypot(s.X-sm.Track[i-1].X, s.Y-sm.Track[i-1].Y) > 0.8 {
			jumps++
		}
		if s.M == 1 {
			refl++ // reflector mode reports 0 or 0.97 by turns: not a measure of the map
			continue
		}
		n++
		sum += s.Conf
		minC = math.Min(minC, s.Conf)
	}
	if n < 5 {
		return nil, fmt.Errorf("the recording %s has only %d samples inside the box: the robot did not drive through it", drive, n)
	}
	need := math.Min(0.5, before+0.1)
	pass := jumps == 0 && minC >= need && minC >= before
	var b strings.Builder
	fmt.Fprintf(&b, "TRIAL RESULT  recording %s, box x %.1f..%.1f y %.1f..%.1f\n", drive, box[0], box[2], box[1], box[3])
	fmt.Fprintf(&b, "%d pass(es), %d samples in the box\n", passes, n)
	fmt.Fprintf(&b, "  lowest confidence   %.2f   (before the fix: %.2f; needed: %.2f or better)\n", minC, before, need)
	fmt.Fprintf(&b, "  average confidence  %.2f\n", sum/float64(n))
	fmt.Fprintf(&b, "  position jumps      %d\n", jumps)
	if pass {
		b.WriteString("\nRESULT: PASS\nA passed trial covers the direction and route that were driven. Drive the other direction too if robots use it.\n")
	} else {
		b.WriteString("\nRESULT: FAIL\nDo not roll this map out. Go back to the fix options.\n")
	}
	return &Result{Summary: b.String(), Data: map[string]any{"pass": pass, "min": minC, "avg": sum / float64(n), "jumps": jumps, "passes": passes, "before": before}}, nil
}

func init() {
	reg(&Tool{Name: "box_evidence", Heavy: true, Desc: "Lay the lasers of one or more recordings over one or more map versions INSIDE A BOX and report what matches: how well each map version fits what the robots really see there, which walls of the first map were seen, and what the lasers see that is not in the map, split into 'in every recording' (permanent) and 'in some only' (passing through). The first map is the one in question; add older versions to find one that fits better. Use after area_report says the place is the problem. A wall that was not seen is not proof it is gone.",
		Params: obj(`"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"x0,y0,x1,y1 in map metres"},"drives":{"type":"array","items":{"type":"string"},"description":"recording names; several made at different times tell permanent from passing objects"},"maps":{"type":"array","items":{"type":"string"},"description":"map names; the first is the map in question"}`, "box", "drives", "maps"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Box    []float64
				Drives []string
				Maps   []string
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if len(p.Box) != 4 {
				return nil, fmt.Errorf("give the box as x0,y0,x1,y1")
			}
			return c.App.boxEvidence(c, [4]float64{min(p.Box[0], p.Box[2]), min(p.Box[1], p.Box[3]), max(p.Box[0], p.Box[2]), max(p.Box[1], p.Box[3])}, p.Drives, p.Maps)
		}})
	reg(&Tool{Name: "trial_result", Desc: "Judge a trial recording inside a box after a fixed map was loaded on one test robot: lowest and average confidence, position jumps, PASS or FAIL against the lowest confidence seen before the fix.",
		Params: obj(pDrive+`,"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4},"before":{"type":"number","description":"lowest confidence in the box before the fix (from area_report)"}`, "drive", "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive  string
				Box    []float64
				Before float64
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if len(p.Box) != 4 {
				return nil, fmt.Errorf("give the box as x0,y0,x1,y1")
			}
			return c.App.trialResult(p.Drive, [4]float64{min(p.Box[0], p.Box[2]), min(p.Box[1], p.Box[3]), max(p.Box[0], p.Box[2]), max(p.Box[1], p.Box[3])}, p.Before)
		}})
}
