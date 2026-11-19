package lasertrack

import (
	"fmt"
	"math"
	"strings"
)

// TrackPt is one pose sample of a recording, for drawing.
type TrackPt struct {
	I    int     `json:"i"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Conf float64 `json:"c"`
	M    int     `json:"m"` // localisation method: 0 laser, 1 reflector
}

// Summary describes a recording using only the robot's own pose reports.
type Summary struct {
	Samples   int       `json:"samples"`
	Start     string    `json:"start"`
	End       string    `json:"end"`
	Distance  float64   `json:"distance"`
	MinConf   float64   `json:"minConfMoving"`
	MinConfAt string    `json:"minConfAt"`
	Stations  []string  `json:"stationLog"`
	Events    []string  `json:"events"`
	LowConf   []string  `json:"lowConfidenceStretches"`
	Track     []TrackPt `json:"track"`
}

// Summarize reads the pose files of a drive (from..to) and lists stations passed, jumps, abnormal
// localisation states and low-confidence stretches. A "jump" is a step of more than 0.25 m at more
// than 1.3 m/s between two samples.
func Summarize(dir string, from, to int) (*Summary, error) {
	n := Count(dir)
	if n == 0 {
		return nil, fmt.Errorf("no recording found in %s", dir)
	}
	if from < 1 {
		from = 1
	}
	if to <= 0 || to > n {
		to = n
	}
	s := &Summary{MinConf: 9}
	var prev *Loc
	var pt float64
	lastSt := ""
	lowStart, lowMin, lowFrom, lowN := -1, 9.0, "", 0
	closeLow := func(i int, where string) {
		if lowStart >= 0 && lowN >= 3 {
			s.LowConf = append(s.LowConf, fmt.Sprintf("samples %d-%d  %s -> %s  lowest %.2f", lowStart, i, lowFrom, where, lowMin))
		}
		lowStart, lowMin, lowN = -1, 9, 0
	}
	type abRun struct {
		key, t0, t1, where string
		i0, i1, n          int
		x, y, conf         float64
	}
	var ab abRun
	flushAb := func() {
		if ab.n > 0 && len(s.Events) < 200 {
			s.Events = append(s.Events, fmt.Sprintf("localisation state not normal: samples %d-%d (%s to %s) at (%.2f, %.2f) near %s: %s, confidence %.2f", ab.i0, ab.i1, ab.t0, ab.t1, ab.x, ab.y, ab.where, ab.key, ab.conf))
		}
		ab = abRun{}
	}
	for i := from; i <= to; i++ {
		l, err := ReadLoc(dir, i)
		if err != nil {
			continue
		}
		s.Samples++
		t := ParseTime(l.CreateOn)
		if s.Start == "" {
			s.Start = l.CreateOn
		}
		s.End = l.CreateOn
		d, dt := 0.0, 0.0
		if prev != nil {
			d, dt = math.Hypot(l.X-prev.X, l.Y-prev.Y), t-pt
		}
		where := l.CurrentStation
		if where == "" {
			where = l.LastStation
		}
		if where == "" {
			where = "(no station)"
		}
		sp := 0.0
		if dt > 0 {
			sp = d / dt
		}
		if prev != nil && d > 0.25 && sp > 1.3 && len(s.Events) < 200 {
			s.Events = append(s.Events, fmt.Sprintf("JUMP: sample %d %s (%.2f, %.2f) step %.2f m in %.2f s, confidence %.2f, near %s", i, clock(l.CreateOn), l.X, l.Y, d, dt, l.Confidence, where))
		}
		// abnormal localisation state: one line per run of samples, not one per sample
		if key := fmt.Sprintf("loc_state %d, update_reason %d", l.LocState, l.UpdateReason); (l.LocState != 1 || l.UpdateReason != 0) && ab.n > 0 && ab.key == key {
			ab.n, ab.i1, ab.t1 = ab.n+1, i, clock(l.CreateOn)
		} else {
			flushAb()
			if l.LocState != 1 || l.UpdateReason != 0 {
				ab = abRun{key: key, i0: i, i1: i, t0: clock(l.CreateOn), t1: clock(l.CreateOn), x: l.X, y: l.Y, conf: l.Confidence, where: where, n: 1}
			}
		}
		moving := d > 0.03
		if moving {
			s.Distance += d
			if l.Confidence < s.MinConf && l.LocMethod == 0 {
				s.MinConf, s.MinConfAt = l.Confidence, fmt.Sprintf("(%.1f, %.1f) near %s, sample %d", l.X, l.Y, where, i)
			}
		}
		// low confidence stretch: laser localisation below 0.5 while moving
		if moving && l.LocMethod == 0 && l.Confidence < 0.5 {
			if lowStart < 0 {
				lowStart, lowFrom = i, where
			}
			lowN++
			lowMin = math.Min(lowMin, l.Confidence)
		} else if moving && lowStart >= 0 {
			closeLow(i-1, where)
		}
		if l.CurrentStation != "" && l.CurrentStation != lastSt {
			s.Stations = append(s.Stations, fmt.Sprintf("%s %-6s (%.2f, %.2f) confidence %.2f", clock(l.CreateOn), l.CurrentStation, l.X, l.Y, l.Confidence))
			lastSt = l.CurrentStation
		}
		s.Track = append(s.Track, TrackPt{I: i, X: l.X, Y: l.Y, Conf: l.Confidence, M: l.LocMethod})
		lc := l
		prev, pt = &lc, t
	}
	closeLow(to, "end")
	flushAb()
	if s.MinConf == 9 {
		s.MinConf = 0
	}
	return s, nil
}

func clock(s string) string {
	if len(s) >= 19 {
		return s[11:19]
	}
	return s
}

// Text renders the summary.
func (s *Summary) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d samples, %s to %s (robot clock), %.0f m driven\n", s.Samples, s.Start, s.End, s.Distance)
	fmt.Fprintf(&b, "lowest laser confidence while moving: %.2f at %s\n", s.MinConf, s.MinConfAt)
	if len(s.Events) == 0 {
		b.WriteString("EVENTS: none (no jump, localisation state normal throughout)\n")
	} else {
		fmt.Fprintf(&b, "EVENTS: %d\n", len(s.Events))
		for i, e := range s.Events {
			if i == 30 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(s.Events)-30)
				break
			}
			b.WriteString("  " + e + "\n")
		}
	}
	if len(s.LowConf) > 0 {
		fmt.Fprintf(&b, "stretches with laser confidence below 0.50 while moving: %d\n", len(s.LowConf))
		for i, e := range s.LowConf {
			if i == 25 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(s.LowConf)-25)
				break
			}
			b.WriteString("  " + e + "\n")
		}
	}
	fmt.Fprintf(&b, "stations passed: %d\n", len(s.Stations))
	st := s.Stations
	if len(st) > 80 {
		for _, e := range st[:40] {
			b.WriteString("  " + e + "\n")
		}
		fmt.Fprintf(&b, "  ... %d more ...\n", len(st)-80)
		st = st[len(st)-40:]
	}
	for _, e := range st {
		b.WriteString("  " + e + "\n")
	}
	return b.String()
}
