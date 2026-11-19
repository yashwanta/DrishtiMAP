package lasertrack

import (
	"fmt"
	"math"
	"strings"

	"drishtimap/smap"
)

// Row is one scan of a replay.
type Row struct {
	I       int     `json:"i"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Deg     float64 `json:"deg"`
	Fit     float64 `json:"fit"`     // % of this scan's laser points that land on a map wall
	Pull    float64 `json:"pull"`    // metres the map placement shifts at this scan
	PullDeg float64 `json:"pullDeg"` // degrees the map placement turns at this scan
	Near    string  `json:"near"`    // nearest station
	Conf    float64 `json:"conf"`    // confidence the robot itself reported during the recording
}

// Stretch is about 8 m of driving matched to the map as one rigid piece.
type Stretch struct {
	I0, I1, Ic int
	Near       string
	X, Y       float64
	Fit        float64    // % of the stretch's laser points on map walls
	Alt        float64    // same for the best different placement (>= 0.5 m away)
	Step       float64    // metres this placement differs from the previous stretch's, at the robot
	TurnDeg    float64    // degrees it differs
	Ambiguous  bool       // another placement fits almost as well: the step here proves nothing
	TrackBreak bool       // the laser track restarts just before this stretch: its step is not a map fault
	T          smap.Rigid // laser track -> map
}

// Seg is the summary of a run of scans.
type Seg struct {
	From, To       string
	I0, I1         int
	AvgFit, MinFit float64
	MaxPull        float64
}

// Replay is the result of driving a recording through a map.
type Replay struct {
	Mode      string    `json:"mode"` // "follow" or "fixed"
	Rows      []Row     `json:"rows"`
	Stretches []Stretch `json:"stretches"`
	Segs      []Seg     `json:"segs"`
	Events    []Stretch `json:"events"` // steps >= 0.25 m
	Jumps     []Stretch `json:"jumps"`  // steps >= 0.5 m at a place that is not ambiguous: a jump on the floor
	AvgFit    float64   `json:"avgFit"`
}

// ReplayOpts tunes a replay.
type ReplayOpts struct {
	Fixed       *smap.Rigid // nil = follow the map. Else: place every scan by the rigid laser track moved by this transform
	StartRadius float64     // how far around the robot's own start pose to look for the start (default 2 m)
	SegLen      int         // scans per summary line (default 30)
	Progress    func(done, total int)
}

// Replay settings. A stretch is +-Half metres of driving around a centre; centres are Stride metres apart.
const (
	Stride     = 1.5
	Half       = 4.0
	Window     = 2.0  // how far a stretch may shift against the previous one
	JumpStep   = 0.5  // a step this large between neighbouring stretches is a jump
	WarnStep   = 0.25 // reported
	priorShare = 0.05 // at the edge of the window a placement must score 5 % of the points better
)

func mapSearch(g *grid, rp []smap.Pt, guess Pose, rxy, rth, sxy, sth float64, step int) Pose {
	ths, ds := steps(rth, sth), steps(rxy, sxy)
	type res struct {
		q Pose
		s int
	}
	rs := make([]res, len(ths))
	parallelDo(len(ths), func(ti int) {
		best := res{guess, -1 << 62}
		for _, dx := range ds {
			for _, dy := range ds {
				q := Pose{guess.X + dx, guess.Y + dy, guess.Th + ths[ti]*math.Pi/180}
				s, _ := g.score(rp, q, step)
				// prefer the predicted pose when scores tie
				s = s*1000 - int(1000*math.Hypot(dx, dy))
				if s > best.s {
					best = res{q, s}
				}
			}
		}
		rs[ti] = best
	})
	best := res{guess, -1}
	for _, r := range rs {
		if r.s > best.s {
			best = r
		}
	}
	return best.q
}

// MapGrid builds the wall grid of a map for replays.
func MapGrid(m *smap.Map) *grid {
	mn, mx := m.Bounds()
	g := newGrid(mn.X-5, mn.Y-5, mx.X+5, mx.Y+5)
	for _, p := range m.Walls() {
		g.add(p, false)
	}
	return g
}

// place finds the shift (dx,dy) and turn (about centre c) that best lays the points on the map.
// pts are already in map coordinates by the previous placement. It returns the correction, the share
// of points on walls, and the same share for the best placement at least 0.5 m away from the winner.
func place(g *grid, pts []smap.Pt, c smap.Pt, R, rth float64) (smap.Rigid, float64, float64) {
	ths, ds := steps(rth, 0.5), steps(R, 0.1)
	nd := len(ds)
	sc := make([]int32, len(ths)*nd*nd)
	step := 1
	if len(pts) > 1600 {
		step = len(pts) / 1600
	}
	n := (len(pts) + step - 1) / step
	parallelDo(len(ths), func(ti int) {
		cs, sn := math.Cos(ths[ti]*math.Pi/180), math.Sin(ths[ti]*math.Pi/180)
		rot := make([]smap.Pt, 0, n)
		for k := 0; k < len(pts); k += step {
			x, y := pts[k].X-c.X, pts[k].Y-c.Y
			rot = append(rot, smap.Pt{X: c.X + cs*x - sn*y, Y: c.Y + sn*x + cs*y})
		}
		for xi, dx := range ds {
			for yi, dy := range ds {
				s := 0
				for _, p := range rot {
					if g.at(p.X+dx, p.Y+dy) > 0 {
						s++
					}
				}
				sc[(ti*nd+xi)*nd+yi] = int32(s)
			}
		}
	})
	// winner: most points on walls, with a mild preference for staying where the previous stretch was
	bi, bs := -1, -1e18
	for i, s := range sc {
		dx, dy := ds[(i/nd)%nd], ds[i%nd]
		d2 := (dx*dx + dy*dy) / (R * R)
		v := float64(s) - priorShare*float64(n)*d2 - 1e-3*math.Sqrt(d2)
		if v > bs {
			bs, bi = v, i
		}
	}
	bth, bdx, bdy := ths[bi/(nd*nd)], ds[(bi/nd)%nd], ds[bi%nd]
	alt := 0
	for i, s := range sc {
		if int(s) > alt && math.Hypot(ds[(i/nd)%nd]-bdx, ds[i%nd]-bdy) >= 0.5 {
			alt = int(s)
		}
	}
	// fine fit with every point
	apply := func(th, dx, dy float64) int {
		cs, sn := math.Cos(th*math.Pi/180), math.Sin(th*math.Pi/180)
		s := 0
		for _, p := range pts {
			x, y := p.X-c.X, p.Y-c.Y
			if g.at(c.X+cs*x-sn*y+dx, c.Y+sn*x+cs*y+dy) > 0 {
				s++
			}
		}
		return s
	}
	fth, fdx, fdy, fs := bth, bdx, bdy, apply(bth, bdx, bdy)
	for _, a := range steps(0.5, 0.1) {
		for _, x := range steps(0.1, 0.025) {
			for _, y := range steps(0.1, 0.025) {
				if s := apply(bth+a, bdx+x, bdy+y); s > fs {
					fth, fdx, fdy, fs = bth+a, bdx+x, bdy+y, s
				}
			}
		}
	}
	th := fth * math.Pi / 180
	cs, sn := math.Cos(th), math.Sin(th)
	// p' = c + R(p-c) + d  ->  as "rotate about origin, then shift"
	t := smap.Rigid{Th: th, Tx: c.X - (cs*c.X - sn*c.Y) + fdx, Ty: c.Y - (sn*c.X + cs*c.Y) + fdy}
	return t, 100 * float64(fs) / float64(len(pts)), 100 * float64(alt) / float64(n)
}

// Run drives the recorded scans through a map.
//
// Follow (default): the drive is cut into overlapping stretches of about 8 m. Each stretch is rigid
// (it comes from the laser track) and is laid on the map where most of its laser points hit walls,
// starting from where the previous stretch lay. Where the map is right, neighbouring stretches agree.
// Where a piece of map is in the wrong place, the placement steps: that is where a robot jumps.
//
// Fixed: every scan is placed by the rigid laser track moved by o.Fixed and only fine-fitted (0.2 m).
// Use it to test "does the whole drive fit this map as ONE rigid track".
func Run(ss []*Sample, ch []ChainPose, m *smap.Map, o ReplayOpts) (*Replay, error) {
	byI := map[int]*Sample{}
	for _, s := range ss {
		byI[s.I] = s
	}
	var cps []ChainPose
	for _, c := range ch {
		if byI[c.I] != nil {
			cps = append(cps, c)
		}
	}
	if len(cps) < 2 {
		return nil, fmt.Errorf("the laser track and the recording do not match")
	}
	g := MapGrid(m)
	stations := m.Stations()
	near := func(x, y float64) string {
		bn, bd := "", 1e9
		for _, a := range stations {
			if d := math.Hypot(a.Pos.X-x, a.Pos.Y-y); d < bd {
				bd, bn = d, a.Name
			}
		}
		return bn
	}
	if o.StartRadius <= 0 {
		o.StartRadius = 2
	}
	if o.SegLen <= 0 {
		o.SegLen = 30
	}
	r := &Replay{Mode: "follow"}
	n := len(cps)
	use := make([]smap.Rigid, n) // placement used for each scan
	restart := make([]bool, n)   // the scan belongs to a stretch that starts a new piece of track
	if o.Fixed != nil {
		r.Mode = "fixed"
		for i := range use {
			use[i] = *o.Fixed
		}
	} else {
		// path distance along the laser track
		dist := make([]float64, n)
		for i := 1; i < n; i++ {
			dist[i] = dist[i-1] + math.Hypot(cps[i].X-cps[i-1].X, cps[i].Y-cps[i-1].Y)
		}
		var centres []int
		next := 0.0
		for i := 0; i < n; i++ {
			if dist[i] >= next {
				centres = append(centres, i)
				next = dist[i] + Stride
			}
		}
		if dist[n-1]-dist[centres[len(centres)-1]] > Stride/2 {
			centres = append(centres, n-1)
		}
		// the laser track is only rigid between breaks; a stretch never reaches across one
		piece := make([]int, n)
		for i := 1; i < n; i++ {
			piece[i] = piece[i-1]
			if cps[i].Break {
				piece[i]++
			}
		}
		prev := smap.Rigid{}
		for ci, ic := range centres {
			if o.Progress != nil {
				o.Progress(ci*n/len(centres)/2, n)
			}
			// scans within +-Half metres of driving, at most about 30, spread evenly
			lo, hi := ic, ic
			for lo > 0 && dist[ic]-dist[lo-1] <= Half && piece[lo-1] == piece[ic] {
				lo--
			}
			for hi < n-1 && dist[hi+1]-dist[ic] <= Half && piece[hi+1] == piece[ic] {
				hi++
			}
			skip := (hi-lo)/30 + 1
			var cloud []smap.Pt
			st := Stretch{I0: cps[lo].I, I1: cps[hi].I, Ic: cps[ic].I}
			for k := lo; k <= hi; k++ {
				if (k-lo)%skip != 0 && k != ic {
					continue
				}
				pose := cps[k].Pose()
				for _, p := range byI[cps[k].I].RP {
					cloud = append(cloud, prev.Apply(tf(p, pose)))
				}
			}
			if len(cloud) > 6000 {
				s2 := len(cloud)/6000 + 1
				var thin []smap.Pt
				for k := 0; k < len(cloud); k += s2 {
					thin = append(thin, cloud[k])
				}
				cloud = thin
			}
			c := prev.Apply(smap.Pt{X: cps[ic].X, Y: cps[ic].Y})
			R, rth := Window, 5.0
			if ci == 0 {
				R, rth = math.Max(o.StartRadius, Window), 8
			} else if piece[ic] != piece[centres[ci-1]] {
				st.TrackBreak = true // new piece of track: find it again from scratch
				R, rth = 3, 12
			}
			corr, fit, alt := place(g, cloud, c, R, rth)
			t := prev.Then(corr)
			st.T, st.Fit, st.Alt = t, fit, alt
			now := t.Apply(smap.Pt{X: cps[ic].X, Y: cps[ic].Y})
			st.X, st.Y, st.Near = now.X, now.Y, near(now.X, now.Y)
			if ci > 0 {
				st.Step = math.Hypot(now.X-c.X, now.Y-c.Y)
				st.TurnDeg = corr.Th * 180 / math.Pi
			}
			st.Ambiguous = fit > 0 && alt >= 0.95*fit
			r.Stretches = append(r.Stretches, st)
			if st.Step >= WarnStep {
				r.Events = append(r.Events, st)
			}
			if st.Step >= JumpStep && !st.Ambiguous && !st.TrackBreak {
				r.Jumps = append(r.Jumps, st)
			}
			prev = t
		}
		// each scan uses the placement of the centre nearest along the drive
		ci := 0
		for i := 0; i < n; i++ {
			for ci+1 < len(centres) && math.Abs(dist[centres[ci+1]]-dist[i]) < math.Abs(dist[centres[ci]]-dist[i]) {
				ci++
			}
			use[i] = r.Stretches[ci].T
			restart[i] = r.Stretches[ci].TrackBreak
		}
	}
	sum := 0.0
	for k, c := range cps {
		if o.Progress != nil && k%10 == 0 {
			o.Progress(n/2+k/2, n)
		}
		s := byI[c.I]
		p := use[k].Apply(smap.Pt{X: c.X, Y: c.Y})
		pred := Pose{p.X, p.Y, c.Th + use[k].Th}
		q := mapSearch(g, s.RP, pred, 0.2, 1.5, 0.05, 0.5, 2)
		q = mapSearch(g, s.RP, q, 0.06, 0.6, 0.02, 0.2, 1)
		_, on := g.score(s.RP, q, 1)
		fit := 100 * float64(on) / float64(len(s.RP))
		pull, pdeg := math.Hypot(q.X-pred.X, q.Y-pred.Y), wrap(q.Th-pred.Th)*180/math.Pi
		if o.Fixed == nil {
			pull, pdeg = 0, 0
			if k > 0 && !(restart[k] && !restart[k-1]) { // a track restart is not a map step
				was := use[k-1].Apply(smap.Pt{X: c.X, Y: c.Y})
				pull, pdeg = math.Hypot(p.X-was.X, p.Y-was.Y), wrap(use[k].Th-use[k-1].Th)*180/math.Pi
			}
		}
		row := Row{I: c.I, X: q.X, Y: q.Y, Deg: wrap(q.Th) * 180 / math.Pi, Fit: fit, Pull: pull, PullDeg: pdeg, Near: near(q.X, q.Y), Conf: c.Conf}
		r.Rows = append(r.Rows, row)
		sum += fit
		si := k / o.SegLen
		if si >= len(r.Segs) {
			r.Segs = append(r.Segs, Seg{From: row.Near, I0: row.I, MinFit: 1e9})
		}
		sg := &r.Segs[si]
		sg.To, sg.I1 = row.Near, row.I
		sg.AvgFit += (fit - sg.AvgFit) / float64(k%o.SegLen+1)
		sg.MinFit = math.Min(sg.MinFit, fit)
		sg.MaxPull = math.Max(sg.MaxPull, pull)
	}
	r.AvgFit = sum / float64(len(r.Rows))
	if o.Progress != nil {
		o.Progress(n, n)
	}
	return r, nil
}

func (e Stretch) counted() bool { return e.Step >= JumpStep && !e.Ambiguous && !e.TrackBreak }

// Text renders the replay summary.
func (r *Replay) Text(label string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s (%s, %d scans, average wall match %.0f%%)\n", label, r.Mode, len(r.Rows), r.AvgFit)
	if r.Mode == "fixed" {
		b.WriteString("scans                         avg match  worst  largest fine-fit shift\n")
	} else {
		b.WriteString("scans                         avg match  worst  largest step\n")
	}
	for _, s := range r.Segs {
		fmt.Fprintf(&b, "  %-7s -> %-7s (%4d-%4d)   %3.0f%%    %3.0f%%   %.2f m\n", s.From, s.To, s.I0, s.I1, s.AvgFit, s.MinFit, s.MaxPull)
	}
	if r.Mode == "fixed" {
		return b.String()
	}
	fmt.Fprintf(&b, "JUMPS (map placement steps %.1f m or more between neighbouring stretches): %d\n", JumpStep, len(r.Jumps))
	for _, e := range r.Jumps {
		fmt.Fprintf(&b, "   near %-6s (%.1f, %.1f) scans %d-%d: step %.2f m, turn %+.1f deg, match %.0f%%\n", e.Near, e.X, e.Y, e.I0, e.I1, e.Step, e.TurnDeg, e.Fit)
	}
	other := len(r.Events) - len(r.Jumps)
	if other > 0 {
		fmt.Fprintf(&b, "other steps of %.2f m or more: %d\n", WarnStep, other)
		k := 0
		for _, e := range r.Events {
			if e.counted() {
				continue
			}
			if k++; k > 25 {
				fmt.Fprintf(&b, "   ... and %d more\n", other-25)
				break
			}
			note := ""
			if e.Ambiguous {
				note += "  [ambiguous place: another position fits as well]"
			}
			if e.TrackBreak {
				note += "  [the laser track restarts here (robot relocalised or samples missing): not a map fault]"
			}
			fmt.Fprintf(&b, "   near %-6s (%.1f, %.1f) scans %d-%d: step %.2f m, match %.0f%%%s\n", e.Near, e.X, e.Y, e.I0, e.I1, e.Step, e.Fit, note)
		}
	}
	return b.String()
}
