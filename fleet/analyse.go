package fleet

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Event is one thing worth telling about a trip.
type Event struct {
	T    time.Time `json:"-"`
	At   string    `json:"at"`
	Kind string    `json:"kind"` // stop, swing, jump, reloc, low, alarm, gap
	X    float64   `json:"x"`
	Y    float64   `json:"y"`
	Text string    `json:"text"`
	Big  bool      `json:"big"`
}

// TrackPt is one drawn point of the trip (same shape as a recording's track).
type TrackPt struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	C float64 `json:"c"`
	M int     `json:"m"`
}

// Stop is a stretch in which the robot did not move.
type Stop struct {
	From, To time.Time
	X, Y     float64
	Deg      float64
	Conf     float64
	MinConf  float64
	Edge     bool // already standing at the start, or still standing at the end, of the time range
	Codes    []string
}

func cminOf(rows []Row) float64 {
	m := 2.0
	for _, w := range rows {
		m = math.Min(m, w.Conf)
	}
	return m
}

// Report is the analysed trip.
type Report struct {
	Trip      *Trip
	Events    []Event
	Stops     []Stop
	Distance  float64
	MinConf   float64
	MinAt     Row
	AvgMoving float64
	Stations  []string
	Maps      []string
	Box       *[4]float64 // where the trouble was (for the comparison with other robots)
	Stalled   int         // seconds in which the history stood still while the robot was driving
	Gaps      int         // seconds without data
	NotConn   int         // seconds the fleet manager marked the robot as not connected
	Relocs    int
	codeText  map[string]string
	near      func(x, y float64) string
}

func dist(a, b Row) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func angDiff(a, b float64) float64 {
	d := math.Mod(a-b+540, 360) - 180
	return math.Abs(d)
}

func dur(d time.Duration) string {
	s := int(d.Seconds() + 0.5)
	switch {
	case s < 90:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min %02d s", s/60, s%60)
	}
	return fmt.Sprintf("%d h %02d min", s/3600, s%3600/60)
}

func hms(t time.Time) string { return t.Format("15:04:05") }

// Analyse finds the stops, heading swings, jumps, low-confidence stretches and alarms of a trip.
// near, when given, names the closest station to a point.
func Analyse(t *Trip, near func(x, y float64) string) *Report {
	r := &Report{Trip: t, MinConf: 2, codeText: map[string]string{}, near: near}
	rows := t.Rows
	at := func(x, y float64) string {
		s := fmt.Sprintf("(%.2f, %.2f)", x, y)
		if near != nil {
			if n := near(x, y); n != "" {
				s += " " + n
			}
		}
		return s
	}
	add := func(e Event) { e.At = hms(e.T); r.Events = append(r.Events, e) }

	// totals
	var movSum float64
	var movN int
	stallEnd := map[int]bool{} // last row of a stretch in which the history stood still
	maps := map[string]bool{}
	for i, w := range rows {
		if !maps[w.Map] && w.Map != "" {
			maps[w.Map] = true
			r.Maps = append(r.Maps, w.Map)
		}
		if w.Station != "" && (len(r.Stations) == 0 || r.Stations[len(r.Stations)-1] != w.Station) {
			r.Stations = append(r.Stations, w.Station)
		}
		if w.Conn != 1 {
			r.NotConn++
		}
		if i == 0 {
			continue
		}
		p := rows[i-1]
		dt := w.T.Sub(p.T)
		d := dist(w, p)
		if dt > 3*time.Second {
			r.Gaps += int(dt.Seconds()) - 1
			add(Event{T: p.T, Kind: "gap", X: p.X, Y: p.Y, Big: dt > 10*time.Second, Text: fmt.Sprintf("no data from the robot for %s (the fleet manager did not hear from it)", dur(dt))})
		}
		// a step no robot can drive in one go: either the history stood still for a while and then
		// caught up (the fleet manager was not hearing from the robot), or the position itself jumped
		v, vPrev := d/math.Max(1, dt.Seconds()), 0.0
		if i >= 2 {
			vPrev = dist(p, rows[i-2]) / math.Max(1, p.T.Sub(rows[i-2].T).Seconds())
		}
		if dt <= 3*time.Second && d >= 0.8 && v > 2*vPrev+0.6 {
			frozen := 0
			for k := i - 1; k > 0 && rows[k-1].X == p.X && rows[k-1].Y == p.Y && rows[k-1].Deg == p.Deg && rows[k-1].Conf == p.Conf; k-- {
				frozen++
			}
			reloc := w.Reloc > 1 || p.Reloc > 1 || (i+1 < len(rows) && rows[i+1].Reloc > 1)
			stalled := frozen >= 1 && d/float64(frozen+1) < 2.0 && math.Abs(w.Conf-p.Conf) < 0.2
			switch {
			case reloc:
				// told below as a relocalisation
			case stalled && frozen < 5:
				r.Distance += d // a second or two late: ordinary
			case stalled:
				r.Stalled += frozen + 1
				stallEnd[i-1] = true
				add(Event{T: rows[i-1-frozen].T, Kind: "gap", X: p.X, Y: p.Y, Big: true, Text: fmt.Sprintf("NO FRESH DATA for %d s: the history stood still at %s and then showed the robot %.1f m further on. The robot kept driving; the fleet manager was not hearing from it (connection stall)", frozen+1, at(p.X, p.Y), d)})
			default:
				add(Event{T: w.T, Kind: "jump", X: w.X, Y: w.Y, Big: true, Text: fmt.Sprintf("position JUMPED %.1f m, from %s to %s; confidence %.2f -> %.2f", d, at(p.X, p.Y), at(w.X, w.Y), p.Conf, w.Conf)})
			}
		} else if d > 0.03 && dt <= 3*time.Second {
			r.Distance += d
			movSum += w.Conf
			movN++
			if w.Conf < r.MinConf {
				r.MinConf, r.MinAt = w.Conf, w
			}
		}
	}
	if movN > 0 {
		r.AvgMoving = movSum / float64(movN)
	} else {
		r.MinConf = 0
	}

	// relocalisation (as reported by the robot)
	for i := 0; i < len(rows); i++ {
		if rows[i].Reloc == 1 || rows[i].Reloc == 0 {
			continue
		}
		j := i
		for j+1 < len(rows) && rows[j+1].Reloc != 1 && rows[j+1].Reloc != 0 {
			j++
		}
		r.Relocs++
		after := rows[j]
		if j+1 < len(rows) {
			after = rows[j+1]
		}
		add(Event{T: rows[i].T, Kind: "reloc", X: after.X, Y: after.Y, Big: true, Text: fmt.Sprintf("the robot was RELOCALISED at %s; confidence after it %.2f", at(after.X, after.Y), after.Conf)})
		i = j
	}

	// stops: 20 s or more within 0.15 m
	for i := 0; i < len(rows); {
		j := i
		for j+1 < len(rows) && dist(rows[j+1], rows[i]) < 0.15 && rows[j+1].T.Sub(rows[j].T) <= 3*time.Second {
			j++
		}
		if d := rows[j].T.Sub(rows[i].T); d >= 20*time.Second && !stallEnd[j] {
			codes := map[string]bool{}
			cmin, cmax := 2.0, -1.0
			for _, w := range rows[i : j+1] {
				for _, c := range w.Codes {
					codes[c] = true
				}
				cmin, cmax = math.Min(cmin, w.Conf), math.Max(cmax, w.Conf)
			}
			s := Stop{From: rows[i].T, To: rows[j].T, X: rows[j].X, Y: rows[j].Y, Deg: rows[j].Deg, Conf: rows[j].Conf, MinConf: cminOf(rows[i : j+1]), Edge: i == 0 || j == len(rows)-1}
			for c := range codes {
				s.Codes = append(s.Codes, c)
			}
			sort.Strings(s.Codes)
			r.Stops = append(r.Stops, s)
			txt := fmt.Sprintf("STOPPED for %s at %s, heading %.0f deg, confidence %.2f to %.2f", dur(d), at(s.X, s.Y), s.Deg, cmin, cmax)
			if i == 0 {
				txt += " (already standing there at the start of the time range)"
			}
			if j == len(rows)-1 {
				txt += " (still standing there at the end of the time range)"
			}
			add(Event{T: s.From, Kind: "stop", X: s.X, Y: s.Y, Big: d >= 2*time.Minute && !s.Edge, Text: txt})
			i = j + 1
		} else {
			i++
		}
	}

	// heading swings while the robot does not move
	for i := 0; i < len(rows); i++ {
		best, bj := 0.0, -1
		for j := i + 1; j < len(rows) && rows[j].T.Sub(rows[i].T) <= 5*time.Second; j++ {
			if dist(rows[j], rows[i]) >= 0.15 {
				break
			}
			if a := angDiff(rows[j].Deg, rows[i].Deg); a > best {
				best, bj = a, j
			}
		}
		// robots turn on the spot at junctions all day; only a turn in which the confidence collapses
		// is worth telling (the position estimate turning, or the robot being pushed round)
		if best >= 15 && rows[i].Conf-rows[bj].Conf >= 0.15 && rows[bj].Conf < 0.5 {
			add(Event{T: rows[i].T, Kind: "swing", X: rows[i].X, Y: rows[i].Y, Big: best >= 25, Text: fmt.Sprintf("heading turned %.0f deg in %s on the spot while confidence FELL %.2f -> %.2f, at %s (%.0f -> %.0f deg)", best, dur(rows[bj].T.Sub(rows[i].T)), rows[i].Conf, rows[bj].Conf, at(rows[i].X, rows[i].Y), rows[i].Deg, rows[bj].Deg)})
			i = bj + 8
		}
	}

	// low confidence stretches (below 0.5 for 3 s or more; short recoveries are bridged)
	type low struct{ a, b int }
	var lows []low
	for i := 0; i < len(rows); i++ {
		if rows[i].Conf >= 0.5 {
			continue
		}
		j := i
		for k := i + 1; k < len(rows) && rows[k].T.Sub(rows[j].T) <= 6*time.Second; k++ {
			if rows[k].Conf < 0.5 {
				j = k
			}
		}
		if rows[j].T.Sub(rows[i].T) >= 3*time.Second {
			lows = append(lows, low{i, j})
		}
		i = j
	}
	type lowBox struct {
		bx       [4]float64
		min      Row
		from, to time.Time
	}
	var lbs []lowBox
	for _, l := range lows {
		m := rows[l.a]
		moved := 0.0
		bx := [4]float64{m.X, m.Y, m.X, m.Y}
		for k := l.a; k <= l.b; k++ {
			if rows[k].Conf < m.Conf {
				m = rows[k]
			}
			if k > l.a {
				moved += dist(rows[k], rows[k-1])
			}
			bx[0], bx[1], bx[2], bx[3] = math.Min(bx[0], rows[k].X), math.Min(bx[1], rows[k].Y), math.Max(bx[2], rows[k].X), math.Max(bx[3], rows[k].Y)
		}
		lbs = append(lbs, lowBox{bx, m, rows[l.a].T, rows[l.b].T})
		add(Event{T: rows[l.a].T, Kind: "low", X: m.X, Y: m.Y, Big: m.Conf < 0.3, Text: fmt.Sprintf("LOW CONFIDENCE until %s (%s, %.0f m driven): lowest %.2f at %s; from %s to %s", hms(rows[l.b].T), dur(rows[l.b].T.Sub(rows[l.a].T)), moved, m.Conf, at(m.X, m.Y), at(rows[l.a].X, rows[l.a].Y), at(rows[l.b].X, rows[l.b].Y))})
	}

	// alarms: first appearance of each code, how often it came back
	type agg struct {
		first, last time.Time
		n           int
		a           Alarm
	}
	byCode := map[string]*agg{}
	var order []string
	for _, a := range t.Alarms {
		if a.Desc != "" {
			r.codeText[a.Code] = a.Desc
		}
		g := byCode[a.Code]
		if g == nil {
			g = &agg{first: a.T, a: a}
			byCode[a.Code] = g
			order = append(order, a.Code)
		}
		g.last = a.T
		g.n++
	}
	for _, c := range order {
		g := byCode[c]
		lvl := map[string]string{"fatals": "FATAL", "errors": "ERROR", "warnings": "warning"}[g.a.Level]
		txt := fmt.Sprintf("%s %s: %s", lvl, c, tidy(g.a.Desc))
		if len(rows) > 0 && g.first.Equal(rows[0].T) {
			txt += " (already active at the start of the time range)"
		}
		if g.n > 1 {
			txt += fmt.Sprintf(" (came %d times, last at %s)", g.n, hms(g.last))
		}
		var x, y float64
		for _, w := range rows {
			if !w.T.Before(g.first) {
				x, y = w.X, w.Y
				break
			}
		}
		add(Event{T: g.first, Kind: "alarm", X: x, Y: y, Big: g.a.Level != "warnings", Text: txt})
	}
	sort.SliceStable(r.Events, func(i, j int) bool { return r.Events[i].T.Before(r.Events[j].T) })

	// the trouble area, for the comparison with other robots: the low-confidence stretch in which
	// the robot stood longest, otherwise the one with the lowest confidence
	pick, longest := -1, time.Duration(0)
	for i, l := range lbs {
		for _, s := range r.Stops {
			if d := s.To.Sub(s.From); !s.Edge && s.MinConf < 0.5 && !s.From.Before(l.from) && !s.From.After(l.to) && d > longest {
				pick, longest = i, d
			}
		}
	}
	if pick < 0 {
		for i, l := range lbs {
			if pick < 0 || l.min.Conf < lbs[pick].min.Conf {
				pick = i
			}
		}
	}
	if pick >= 0 {
		bx, c := lbs[pick].bx, lbs[pick].min
		if bx[2]-bx[0] > 25 || bx[3]-bx[1] > 25 {
			bx = [4]float64{c.X - 8, c.Y - 8, c.X + 8, c.Y + 8}
		} else {
			bx = [4]float64{bx[0] - 1, bx[1] - 1, bx[2] + 1, bx[3] + 1}
		}
		r.Box = &bx
	} else {
		for _, e := range r.Events {
			if e.Kind == "jump" || e.Kind == "swing" {
				r.Box = &[4]float64{e.X - 5, e.Y - 5, e.X + 5, e.Y + 5}
				break
			}
		}
	}
	return r
}

// tidy makes the robot's own alarm text readable (it sometimes carries a format string and numbers).
func tidy(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "[\""); i >= 0 {
		head := strings.TrimSpace(s[:i])
		rest := strings.Trim(s[i:], "[]")
		parts := strings.Split(rest, "\",")
		if len(parts) == 2 {
			f := strings.Trim(parts[0], "\" ")
			for k, v := range strings.Split(parts[1], ",") {
				f = strings.ReplaceAll(f, fmt.Sprintf("{%d}", k+1), fmt.Sprintf("%.2f", pf(strings.TrimSpace(v))))
			}
			s = strings.TrimSpace(head + " " + f)
		}
	}
	return strings.TrimRight(s, ", ")
}

// Track returns the trip as points to draw (at most about 4000).
func (r *Report) Track() []TrackPt {
	rows := r.Trip.Rows
	step := len(rows)/4000 + 1
	var out []TrackPt
	for i := 0; i < len(rows); i += step {
		out = append(out, TrackPt{X: rows[i].X, Y: rows[i].Y, C: rows[i].Conf})
	}
	return out
}

// Text is the plain report of the trip.
func (r *Report) Text() string {
	t := r.Trip
	var b strings.Builder
	fmt.Fprintf(&b, "WHAT HAPPENED: %s, %s to %s (fleet manager time)\n", t.Robot, t.From.Format("2006-01-02 15:04"), t.To.Format("15:04"))
	fmt.Fprintf(&b, "read-only, from the fleet manager's history: %d seconds of data", len(t.Rows))
	if len(r.Maps) > 0 {
		fmt.Fprintf(&b, ", map %s", strings.Join(r.Maps, " then "))
	}
	b.WriteString("\n")
	if r.Distance < 0.5 {
		fmt.Fprintf(&b, "The robot did not drive in this time: it stood at (%.2f, %.2f), confidence %.2f.\n", t.Rows[0].X, t.Rows[0].Y, t.Rows[0].Conf)
	} else {
		fmt.Fprintf(&b, "drove %.0f m; confidence while moving: average %.2f, lowest %.2f at (%.2f, %.2f) %s\n", r.Distance, r.AvgMoving, r.MinConf, r.MinAt.X, r.MinAt.Y, hms(r.MinAt.T))
	}
	if len(r.Stations) > 0 {
		st := r.Stations
		if len(st) > 40 {
			st = append(append([]string{}, st[:20]...), append([]string{"..."}, st[len(st)-18:]...)...)
		}
		fmt.Fprintf(&b, "stations reached: %s\n", strings.Join(st, " "))
	}
	b.WriteString("\nTIMELINE\n")
	if len(r.Events) == 0 {
		b.WriteString("  nothing unusual: no stop of 20 s or more, no jump, no relocalisation, no alarm, confidence 0.5 or better throughout\n")
	}
	shown := 0
	for _, e := range r.Events {
		if shown == 60 {
			fmt.Fprintf(&b, "  ... %d more entries not shown; choose a shorter time range\n", len(r.Events)-shown)
			break
		}
		mark := "  "
		if e.Big {
			mark = "! "
		}
		fmt.Fprintf(&b, "%s%s  %s\n", mark, e.At, e.Text)
		shown++
	}
	b.WriteString("\nCONNECTION TO THE FLEET MANAGER\n")
	switch {
	case r.Gaps == 0 && r.NotConn == 0 && t.Missing == 0 && r.Stalled == 0:
		b.WriteString("  no drop seen: fresh data in every second while the robot was driving\n")
	default:
		fmt.Fprintf(&b, "  %d s with no fresh data while driving (stalls, see the timeline), %d s without any data, %d s marked as not connected, %d s missing from the fleet manager's list\n", r.Stalled, r.Gaps, r.NotConn, t.Missing)
	}
	b.WriteString("  (a stall can only be seen while the robot is moving; while it stands, unchanged data looks the same as no data)\n")
	return b.String()
}

// LaneText compares all robots' passes through the trouble area.
// Compare is the outcome of the comparison with other robots.
type Compare struct {
	Others   int     // other robots that drove through the area
	Typical  float64 // the middle of the other robots' lowest confidence there
	Mine     float64 // this robot's lowest confidence there
	BadPass  int     // other robots with at least one pass below 0.35
	Marginal bool    // typical passes are fine, but most robots had a bad one
	Weak     bool    // the area is weak for the fleet
	Worse    bool    // this robot did clearly worse than the others
}

func LaneText(passes []Pass, robot string, box [4]float64, from, to time.Time) (string, Compare) {
	var b strings.Builder
	fmt.Fprintf(&b, "\nOTHER ROBOTS IN THE SAME AREA  x %.1f..%.1f  y %.1f..%.1f,  %s to %s\n", box[0], box[2], box[1], box[3], from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
	type agg struct {
		n, long, errs, reloc int
		min, avg, worst      float64
	}
	by := map[string]*agg{}
	var names []string
	for _, p := range passes {
		if p.Seconds < 6 {
			continue
		}
		a := by[p.Robot]
		if a == nil {
			a = &agg{worst: 2}
			by[p.Robot] = a
			names = append(names, p.Robot)
		}
		a.n++
		a.min += p.MinConf
		a.avg += p.AvgConf
		a.worst = math.Min(a.worst, p.MinConf)
		if p.Still >= 60 {
			a.long++
		}
		if len(p.Codes) > 0 {
			a.errs++
		}
		a.reloc += p.Reloc
	}
	sort.Strings(names)
	if len(names) == 0 {
		b.WriteString("  no robot drove through this area in that time\n")
		return b.String(), Compare{}
	}
	fmt.Fprintf(&b, "  %-9s %6s  %-14s %-14s %-12s %-11s %s\n", "robot", "passes", "lowest (mean)", "lowest (worst)", "stops >1 min", "with errors", "relocalised")
	cmp := Compare{Mine: -1}
	var mins []float64
	for _, n := range names {
		a := by[n]
		mark := ""
		if n == robot {
			mark = "  <-- this robot"
			cmp.Mine = a.worst
		} else {
			cmp.Others++
			mins = append(mins, a.min/float64(a.n))
		}
		fmt.Fprintf(&b, "  %-9s %6d  %-14.2f %-14.2f %-12d %-11d %d%s\n", n, a.n, a.min/float64(a.n), a.worst, a.long, a.errs, a.reloc, mark)
	}
	if cmp.Others == 0 {
		b.WriteString("  Only this robot drove here in that time, so there is nothing to compare with.\n")
		return b.String(), cmp
	}
	sort.Float64s(mins)
	cmp.Typical = mins[len(mins)/2]
	if len(mins)%2 == 0 {
		cmp.Typical = (mins[len(mins)/2-1] + mins[len(mins)/2]) / 2
	}
	for _, n := range names {
		if n != robot && by[n].worst < 0.35 {
			cmp.BadPass++
		}
	}
	cmp.Marginal = cmp.Typical >= 0.5 && cmp.BadPass*2 >= cmp.Others
	cmp.Weak = cmp.Typical < 0.5
	cmp.Worse = cmp.Mine >= 0 && cmp.Mine < cmp.Typical-0.15
	fmt.Fprintf(&b, "  On a typical pass the %d other robots fall to %.2f here (from %.2f to %.2f); %d of them had at least one pass below 0.35", cmp.Others, cmp.Typical, mins[0], mins[len(mins)-1], cmp.BadPass)
	if cmp.Mine >= 0 {
		fmt.Fprintf(&b, ". %s fell to %.2f", robot, cmp.Mine)
	}
	b.WriteString(".\n")
	switch {
	case cmp.Weak && cmp.Worse:
		fmt.Fprintf(&b, "  The area is weak for the whole fleet (below 0.5), and %s went clearly lower than the others on this trip.\n", robot)
	case cmp.Weak:
		fmt.Fprintf(&b, "  The area is weak for the whole fleet (below 0.5), not only for %s.\n", robot)
	case cmp.Marginal:
		fmt.Fprintf(&b, "  The area is marginal: robots usually get through, but most of them have lost their footing here at least once.\n")
	case cmp.Worse:
		fmt.Fprintf(&b, "  The other robots keep their confidence here: look at %s itself (scanners, wheels) or at what stood there at that moment.\n", robot)
	default:
		b.WriteString("  Nothing stands out in this area.\n")
	}
	return b.String(), cmp
}
