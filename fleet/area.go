package fleet

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Area is what every robot did inside a box over a time range.
type Area struct {
	Passes []Pass
	Points []AreaPt           // drawn points of the passes
	Global map[string]float64 // each robot's average confidence while moving, anywhere in the plant
	Pushes []time.Time        // when a map was pushed to the fleet
	Codes  map[string]string  // error code -> the robot's own text
	Box    [4]float64
	From   time.Time
	To     time.Time
}

// AreaPt is one drawn point of a pass.
type AreaPt struct {
	R string  `json:"r"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
	C float64 `json:"c"`
}

// ParseArea reads the lines printed by history.py in lane mode, with its extra lines.
func ParseArea(lines []string) *Area {
	a := &Area{Passes: ParseLane(lines), Global: map[string]float64{}, Codes: map[string]string{}}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		switch {
		case f[0] == "A" && len(f) >= 5:
			a.Points = append(a.Points, AreaPt{f[1], pf(f[2]), pf(f[3]), pf(f[4])})
		case f[0] == "G" && len(f) >= 4:
			if n := pf(f[2]); n >= 300 {
				a.Global[f[1]] = pf(f[3]) / n
			}
		case f[0] == "S" && len(f) >= 2:
			if t, err := time.ParseInLocation("2006-01-02_15-04-05", f[1], time.Local); err == nil {
				a.Pushes = append(a.Pushes, t)
			}
		case f[0] == "C" && len(f) >= 3:
			a.Codes[f[1]] = tidy(f[2])
		}
	}
	return a
}

// Area reads every robot's passes through a box between two times.
func (s Source) Area(ctx context.Context, box [4]float64, from, to time.Time, prog func(done, total int)) (*Area, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("the end time must be after the start time")
	}
	if to.Sub(from) > 8*24*time.Hour {
		return nil, fmt.Errorf("choose 7 days or less")
	}
	if box[2]-box[0] < 0.5 || box[3]-box[1] < 0.5 {
		return nil, fmt.Errorf("the box is too small; draw it at least 0.5 m wide and high")
	}
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	lines, err := s.run(ctx, prog, "lane", s.dir(), n(box[0]), n(box[1]), n(box[2]), n(box[3]), stamp(from), stamp(to), "2")
	if err != nil {
		return nil, err
	}
	a := ParseArea(lines)
	a.Box, a.From, a.To = box, from, to
	return a, nil
}

// RobotRow is one robot's line in the area table.
type RobotRow struct {
	Robot     string   `json:"robot"`
	Passes    int      `json:"passes"`
	Worst     float64  `json:"worst"`
	MeanMin   float64  `json:"meanMin"`
	Bad       int      `json:"bad"` // passes below 0.3
	Low       int      `json:"low"` // passes 0.3 to 0.5
	Stops     int      `json:"stops"`
	Reloc     int      `json:"reloc"`
	Errors    []string `json:"errors"`
	Dir       string   `json:"dir"`
	Elsewhere float64  `json:"elsewhere"` // average confidence while moving anywhere; 0 = not known
	LowAll    bool     `json:"lowAll"`    // low everywhere, not only in this box
}

// AreaReport is the analysed area: the table, the patterns and one verdict.
type AreaReport struct {
	Robots   []RobotRow `json:"robots"`
	Passes   int        `json:"passes"`
	Fine     int        `json:"fine"`
	Low      int        `json:"low"`
	Bad      int        `json:"bad"`
	Typical  float64    `json:"typical"` // middle of the robots' mean lowest confidence
	Verdict  string     `json:"verdict"`
	Kind     string     `json:"kind"` // place, marginal, robot, scanner, oneoff, mapchange, nothing, nodata
	Rating   string     `json:"rating"`
	Summary  string     `json:"summary"`
	Why      []string   `json:"why"`
	Ruled    []string   `json:"ruled"`
	Unknown  []string   `json:"unknown"`
	Patterns []string   `json:"patterns"`
	Events   []string   `json:"events"`
	IsMap    bool       `json:"isMap"`
	Coarse   bool       `json:"coarse"` // from AMR Health by station, not from the second-by-second history // worth going on to laser evidence and a map fix
}

func dirOf(dx, dy float64) string {
	if math.Hypot(dx, dy) < 1.0 {
		return ""
	}
	if math.Abs(dy) >= math.Abs(dx) {
		if dy > 0 {
			return "north"
		}
		return "south"
	}
	if dx > 0 {
		return "east"
	}
	return "west"
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64{}, v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// AnalyseArea turns the passes into the table, the patterns and a verdict with a rating that comes
// from counts (robots, passes), never from a guess.
func AnalyseArea(a *Area, focus string) *AreaReport {
	r := &AreaReport{Robots: []RobotRow{}}
	type acc struct {
		row  *RobotRow
		dirs map[string]int
		errs map[string]bool
	}
	by := map[string]*acc{}
	var names []string
	var passes []Pass
	for _, p := range a.Passes {
		if p.Seconds < 6 {
			continue // clipped a corner of the box
		}
		passes = append(passes, p)
		x := by[p.Robot]
		if x == nil {
			x = &acc{row: &RobotRow{Robot: p.Robot, Worst: 2}, dirs: map[string]int{}, errs: map[string]bool{}}
			by[p.Robot] = x
			names = append(names, p.Robot)
		}
		w := x.row
		w.Passes++
		w.MeanMin += p.MinConf
		w.Worst = math.Min(w.Worst, p.MinConf)
		switch {
		case p.MinConf < 0.3:
			w.Bad++
			r.Bad++
		case p.MinConf < 0.5:
			w.Low++
			r.Low++
		default:
			r.Fine++
		}
		if p.Still >= 60 {
			w.Stops++
			r.Events = append(r.Events, fmt.Sprintf("%s %s stood %s in the box (lowest confidence %.2f)", p.T0.Format("02 Jan 15:04"), p.Robot, dur(time.Duration(p.Still)*time.Second), p.MinConf))
		}
		w.Reloc += p.Reloc
		if p.Reloc > 0 {
			r.Events = append(r.Events, fmt.Sprintf("%s %s was relocalised in the box", p.T0.Format("02 Jan 15:04"), p.Robot))
		}
		for _, c := range p.Codes {
			x.errs[c] = true
		}
		if len(p.Codes) > 0 {
			var t []string
			for _, c := range p.Codes {
				t = append(t, strings.TrimSpace(c+" "+a.Codes[c]))
			}
			r.Events = append(r.Events, fmt.Sprintf("%s %s reported: %s", p.T0.Format("02 Jan 15:04"), p.Robot, strings.Join(t, "; ")))
		}
		if d := dirOf(p.DX, p.DY); d != "" {
			x.dirs[d]++
		}
	}
	r.Passes = len(passes)
	sort.Strings(names)
	var glob []float64
	for _, v := range a.Global {
		glob = append(glob, v)
	}
	fleetAvg := median(glob)
	var mins []float64
	for _, n := range names {
		x := by[n]
		w := x.row
		w.MeanMin /= float64(w.Passes)
		mins = append(mins, w.MeanMin)
		for c := range x.errs {
			w.Errors = append(w.Errors, strings.TrimSpace(c+" "+a.Codes[c]))
		}
		sort.Strings(w.Errors)
		var ds []string
		for d := range x.dirs {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		w.Dir = strings.Join(ds, ", ")
		w.Elsewhere = a.Global[n]
		w.LowAll = w.Elsewhere > 0 && fleetAvg > 0 && w.Elsewhere < fleetAvg-0.1
		r.Robots = append(r.Robots, *w)
	}
	sort.SliceStable(r.Robots, func(i, j int) bool { return r.Robots[i].Worst < r.Robots[j].Worst })
	r.Typical = median(mins)
	nRob := len(names)

	// rating from counts
	switch {
	case nRob >= 5 && r.Passes >= 30:
		r.Rating = "HIGH"
	case nRob >= 3 && r.Passes >= 10:
		r.Rating = "MEDIUM"
	default:
		r.Rating = "LOW"
	}
	counts := fmt.Sprintf("%d robots, %d passes through the box", nRob, r.Passes)

	// patterns
	trouble := func(p Pass) bool { return p.MinConf < 0.5 }
	dirT, dirF := map[string]int{}, map[string]int{}
	nT := 0
	for _, p := range passes {
		d := dirOf(p.DX, p.DY)
		if d == "" {
			continue
		}
		if trouble(p) {
			dirT[d]++
			nT++
		} else {
			dirF[d]++
		}
	}
	oneWay := "" // the direction of travel that carries the trouble, if one does
	for d, n := range dirT {
		if nT >= 5 && float64(n) >= 0.75*float64(nT) && dirF[d]*2 < r.Fine+1 {
			oneWay = d
			r.Patterns = append(r.Patterns, fmt.Sprintf("Direction: %d of %d troubled passes were heading %s (%d passes heading %s were fine). Look at what the front scanner faces when driving %s here", n, nT, d, dirF[d], d, d))
		}
	}
	if len(r.Patterns) == 0 && nT >= 5 {
		r.Patterns = append(r.Patterns, "Direction: no link (trouble in both directions of travel)")
	}
	block := map[int]int{}
	for _, p := range passes {
		if trouble(p) {
			block[p.T0.Hour()/6]++
		}
	}
	names6 := []string{"night (00-06)", "morning (06-12)", "afternoon (12-18)", "evening (18-24)"}
	hit := false
	for b, n := range block {
		if nT >= 6 && float64(n) >= 0.75*float64(r.Low+r.Bad) {
			r.Patterns = append(r.Patterns, fmt.Sprintf("Time of day: %d of %d troubled passes were in the %s", n, r.Low+r.Bad, names6[b]))
			hit = true
		}
	}
	if !hit && r.Low+r.Bad >= 6 {
		r.Patterns = append(r.Patterns, "Time of day: no link")
	}
	// map change inside the time range
	// Like is compared with like: when one direction carries the trouble, only passes in that
	// direction are compared, otherwise a night with different traffic looks like a change.
	// Every push in the range is tried; the one with the largest drop is reported.
	mapChange := false
	var pushes []time.Time
	for _, t := range a.Pushes {
		if t.After(a.From) && t.Before(a.To) && (len(pushes) == 0 || t.Sub(pushes[len(pushes)-1]) > 10*time.Minute) {
			pushes = append(pushes, t)
		}
	}
	var cmpP []Pass
	for _, p := range passes {
		if oneWay == "" || dirOf(p.DX, p.DY) == oneWay {
			cmpP = append(cmpP, p)
		}
	}
	which := ""
	if oneWay != "" {
		which = " (passes heading " + oneWay + ")"
	}
	bestDrop, tested := 0.0, 0
	var bestT time.Time
	var bb, ba float64
	for _, push := range pushes {
		var before, after []float64
		for _, p := range cmpP {
			if p.T0.Before(push) {
				before = append(before, p.MinConf)
			} else {
				after = append(after, p.MinConf)
			}
		}
		if len(before) < 4 || len(after) < 4 {
			continue
		}
		tested++
		if d := median(before) - median(after); bestT.IsZero() || d > bestDrop {
			bestDrop, bestT, bb, ba = d, push, median(before), median(after)
		}
	}
	switch {
	case len(pushes) == 0:
	case tested == 0:
		r.Patterns = append(r.Patterns, fmt.Sprintf("Map changes: %d in this time range, but too few passes on one side of each to compare", len(pushes)))
	case bestDrop >= 0.12:
		mapChange = true
		r.Patterns = append(r.Patterns, fmt.Sprintf("Map change on %s: typical lowest confidence%s was %.2f before and is %.2f after: WORSE since then (%d map change(s) in this time range)", bestT.Format("02 Jan 15:04"), which, bb, ba, len(pushes)))
	case bestDrop <= -0.12:
		r.Patterns = append(r.Patterns, fmt.Sprintf("Map change on %s: typical lowest confidence%s was %.2f before and is %.2f after: better since then", bestT.Format("02 Jan 15:04"), which, bb, ba))
	default:
		r.Patterns = append(r.Patterns, fmt.Sprintf("Map changes: %d in this time range, no difference before and after any of them", len(pushes)))
		r.Ruled = append(r.Ruled, "The map changes in this time range: confidence here is the same before and after each")
	}

	// who carries the trouble
	withBad, troubled := 0, 0
	var worstRobots []RobotRow
	for _, w := range r.Robots {
		if w.Bad > 0 {
			withBad++
		}
		if w.Bad+w.Low > 0 {
			troubled++
			worstRobots = append(worstRobots, w)
		}
	}
	clean := 0 // robots with 3 or more passes and none troubled
	for _, w := range r.Robots {
		if w.Passes >= 3 && w.Bad+w.Low == 0 {
			clean++
		}
	}
	var lowAll []string
	for _, w := range r.Robots {
		if w.LowAll {
			lowAll = append(lowAll, w.Robot)
		}
	}

	set := func(kind, verdict, summary string, isMap bool) {
		r.Kind, r.Verdict, r.Summary, r.IsMap = kind, verdict, summary, isMap
	}
	switch {
	case r.Passes < 5 || nRob < 2:
		r.Rating = "LOW"
		set("nodata", "Not enough data", fmt.Sprintf("Only %d pass(es) by %d robot(s) in this box in that time. Choose a longer time or a larger box.", r.Passes, nRob), false)
	case r.Low+r.Bad == 0:
		set("nothing", "Nothing stands out", "Every pass through this box kept its confidence at 0.5 or better.", false)
		r.Why = append(r.Why, counts)
	case troubled <= 2 && clean >= 3:
		w := worstRobots[0]
		names := []string{}
		for _, x := range worstRobots {
			names = append(names, x.Robot)
		}
		if w.LowAll {
			set("scanner", "One robot, everywhere: "+strings.Join(names, ", "), fmt.Sprintf("%s is low here and also low in the rest of the plant (%.2f on average while moving, fleet %.2f). Look at its scanners (clean, level, at the right height) before the map.", w.Robot, w.Elsewhere, fleetAvg), false)
		} else {
			set("robot", "One robot, at this place: "+strings.Join(names, ", "), fmt.Sprintf("Only %s had trouble here; %d other robots with 3 or more passes each kept their confidence. Something at that moment (a blockage, a slip, a load) or that robot, not the map.", strings.Join(names, " and "), clean), false)
		}
		r.Why = append(r.Why, counts, fmt.Sprintf("%d robot(s) troubled, %d robots clean with 3 or more passes", troubled, clean))
	case mapChange:
		set("mapchange", "The place: worse since "+bestT.Format("02 Jan 15:04"), fmt.Sprintf("Confidence in this box dropped from %.2f to %.2f around a map change on %s. That change can be the cause, or something was moved on the floor at about the same time: the next step compares the map versions in this box to tell.", bb, ba, bestT.Format("02 Jan 15:04")), true)
		r.Why = append(r.Why, counts, fmt.Sprintf("%d fine, %d low, %d bad passes", r.Fine, r.Low, r.Bad))
		if len(pushes) > 1 && r.Rating == "HIGH" {
			r.Rating = "MEDIUM" // several map changes close together: the link to one of them is weaker
		}
	case r.Typical < 0.5:
		set("place", "The place: weak for the whole fleet", fmt.Sprintf("On a typical pass robots fall to %.2f here. What stands there no longer matches the map, or there is little for the lasers to see.", r.Typical), true)
		r.Why = append(r.Why, counts, fmt.Sprintf("%d of %d robots had a troubled pass", troubled, nRob))
	case withBad*5 >= nRob*2 || (r.Low+r.Bad)*5 >= r.Passes:
		set("marginal", "The place: marginal", fmt.Sprintf("Robots usually get through (typically %.2f), but %d of %d have had a pass below 0.3 here. It leaves little margin: one more thing going wrong is enough to lose position.", r.Typical, withBad, nRob), true)
		r.Why = append(r.Why, counts, fmt.Sprintf("%d fine, %d low, %d bad passes", r.Fine, r.Low, r.Bad))
	default:
		set("oneoff", "One-off events", fmt.Sprintf("%d of %d passes were troubled, spread over %d robots, with no pattern. More likely things passing through (a tow motor, a person, a bin set down) than the map.", r.Low+r.Bad, r.Passes, troubled), false)
		r.Why = append(r.Why, counts)
	}
	if focus != "" {
		if x := by[focus]; x != nil {
			r.Why = append(r.Why, fmt.Sprintf("%s: %d passes, lowest %.2f, %d below 0.3", focus, x.row.Passes, x.row.Worst, x.row.Bad))
		}
	}
	if len(lowAll) == 0 && nRob >= 3 {
		r.Ruled = append(r.Ruled, "One robot's scanner: no robot is low everywhere in the plant")
	} else if len(lowAll) > 0 && r.Kind != "scanner" {
		r.Why = append(r.Why, "Low everywhere in the plant, not only here: "+strings.Join(lowAll, ", ")+" (check their scanners)")
	}
	if r.IsMap {
		r.Unknown = append(r.Unknown, "Which walls do not match. The history has no laser data; the next step needs a recording through the box.")
	}
	if len(r.Events) > 12 {
		r.Events = append(r.Events[:12], fmt.Sprintf("... %d more", len(r.Events)-12))
	}
	return r
}

// Text is the plain report (also what the assistant reads).
func (r *AreaReport) Text(a *Area) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AREA REPORT  x %.1f..%.1f  y %.1f..%.1f,  %s to %s (fleet manager time)\n", a.Box[0], a.Box[2], a.Box[1], a.Box[3], a.From.Format("2006-01-02 15:04"), a.To.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "%d passes by %d robots: %d fine, %d low (0.3 to 0.5), %d bad (below 0.3)\n\n", r.Passes, len(r.Robots), r.Fine, r.Low, r.Bad)
	fmt.Fprintf(&b, "VERDICT: %s   [rating %s]\n%s\n", r.Verdict, r.Rating, r.Summary)
	sec := func(t string, l []string) {
		if len(l) > 0 {
			b.WriteString("\n" + t + "\n")
			for _, x := range l {
				b.WriteString("  - " + x + "\n")
			}
		}
	}
	sec("WHY", r.Why)
	sec("PATTERNS", r.Patterns)
	sec("RULED OUT", r.Ruled)
	sec("NOT KNOWN YET", r.Unknown)
	sec("EVENTS", r.Events)
	fmt.Fprintf(&b, "\n%-9s %6s %7s %9s %5s %5s %6s %-13s %s\n", "robot", "passes", "lowest", "mean low", "bad", "stops", "reloc", "direction", "elsewhere")
	for _, w := range r.Robots {
		e := "-"
		if w.Elsewhere > 0 {
			e = fmt.Sprintf("%.2f", w.Elsewhere)
			if w.LowAll {
				e += " LOW EVERYWHERE"
			}
		}
		fmt.Fprintf(&b, "%-9s %6d %7.2f %9.2f %5d %5d %6d %-13s %s\n", w.Robot, w.Passes, w.Worst, w.MeanMin, w.Bad, w.Stops, w.Reloc, w.Dir, e)
	}
	return b.String()
}
