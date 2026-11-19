package lasertrack

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"drishtimap/smap"
)

// ChainPose is one sample of the rigid laser track.
type ChainPose struct {
	I       int     `json:"i"`
	BelX    float64 `json:"bx"` // pose the robot believed
	BelY    float64 `json:"by"`
	BelTh   float64 `json:"bth"`
	Conf    float64 `json:"conf"`
	X       float64 `json:"x"` // pose from the lasers alone
	Y       float64 `json:"y"`
	Th      float64 `json:"th"`
	Fit     float64 `json:"fit"`   // % of points matching the scans before it
	Alt     float64 `json:"alt"`   // best score of a different position (>= 0.4 m away): close to Fit = ambiguous place
	Jumpy   bool    `json:"jumpy"` // the robot's own pose jumped here (relocalisation)
	Break   bool    `json:"brk"`   // this scan could not be matched to the scans before it: the track restarts here
	Station string  `json:"st"`
	T       float64 `json:"t"`
}

func (c ChainPose) Pose() Pose { return Pose{c.X, c.Y, c.Th} }
func (c ChainPose) Bel() Pose  { return Pose{c.BelX, c.BelY, c.BelTh} }

type cand struct {
	q Pose
	s int
}

// chainSearch finds the best pose around guess; it also returns the best score at a different place.
func chainSearch(g *grid, rp []smap.Pt, guess Pose, rxy, rth float64) (Pose, int, int) {
	ths, ds := steps(rth, 0.5), steps(rxy, 0.05)
	per := len(ds) * len(ds)
	cs := make([]cand, len(ths)*per)
	parallelDo(len(ths), func(ti int) {
		k := ti * per
		for _, dx := range ds {
			for _, dy := range ds {
				q := Pose{guess.X + dx, guess.Y + dy, guess.Th + ths[ti]*math.Pi/180}
				s, _ := g.score(rp, q, 2)
				cs[k] = cand{q, s}
				k++
			}
		}
	})
	best := cand{guess, -1}
	for _, c := range cs {
		if c.s > best.s {
			best = c
		}
	}
	alt := 0
	for _, c := range cs {
		if c.s > alt && math.Hypot(c.q.X-best.q.X, c.q.Y-best.q.Y) >= 0.4 {
			alt = c.s
		}
	}
	fb := best.q
	fs, _ := g.score(rp, fb, 1)
	for _, dth := range steps(0.5, 0.1) {
		for _, dx := range steps(0.05, 0.0125) {
			for _, dy := range steps(0.05, 0.0125) {
				q := Pose{best.q.X + dx, best.q.Y + dy, best.q.Th + dth*math.Pi/180}
				if s, _ := g.score(rp, q, 1); s > fs {
					fs, fb = s, q
				}
			}
		}
	}
	return fb, fs, alt * 2
}

// BreakFit: below this match (%) against the scans before it, a scan starts a new piece of track.
const BreakFit = 40.0

// ChainOpts tunes the laser track.
type ChainOpts struct {
	SkipStill bool // do not re-match while the robot stands still (saves time on long recordings)
	Progress  func(done, total int)
}

// Chain rebuilds the true shape of a drive by matching every scan to the scans just before it.
// No map is used, so the result is not affected by any map error. Over a long loop it drifts a
// little (about 0.1 m over 80 m in practice); it is rigid over tens of metres.
func Chain(ss []*Sample, o ChainOpts) []ChainPose {
	minX, minY, maxX, maxY := 1e9, 1e9, -1e9, -1e9
	for _, s := range ss {
		minX, minY = math.Min(minX, s.Bel.X), math.Min(minY, s.Bel.Y)
		maxX, maxY = math.Max(maxX, s.Bel.X), math.Max(maxY, s.Bel.Y)
	}
	g := newGrid(minX-45, minY-45, maxX+45, maxY+45)
	type kf struct {
		pose Pose
		rp   []smap.Pt
	}
	poses := make([]Pose, len(ss))
	out := make([]ChainPose, len(ss))
	brk := make([]bool, len(ss))
	mk := func(k int, score, alt int, jumpy bool) {
		s := ss[k]
		out[k] = ChainPose{I: s.I, BelX: s.Bel.X, BelY: s.Bel.Y, BelTh: s.Bel.Th, Conf: s.Conf, X: poses[k].X, Y: poses[k].Y, Th: wrap(poses[k].Th),
			Fit: 50 * float64(score) / float64(len(s.RP)), Alt: 50 * float64(alt) / float64(len(s.RP)), Jumpy: jumpy, Break: brk[k], Station: s.Station, T: s.T}
	}
	poses[0] = ss[0].Bel
	mk(0, 0, 0, false)
	kfs := []kf{{poses[0], ss[0].RP}}
	lastScore, lastAlt := 0, 0
	for k := 1; k < len(ss); k++ {
		if o.Progress != nil && k%10 == 0 {
			o.Progress(k, len(ss))
		}
		s, pv := ss[k], ss[k-1]
		d := rel(pv.Bel, s.Bel)
		dt := s.T - pv.T
		if o.SkipStill && math.Hypot(d.X, d.Y) < 0.004 && math.Abs(d.Th) < 0.002 && k > 1 {
			poses[k] = poses[k-1]
			mk(k, lastScore*len(s.RP)/max(1, len(pv.RP)), lastAlt*len(s.RP)/max(1, len(pv.RP)), false)
			continue
		}
		guess := compose(poses[k-1], d)
		rxy, rth, jumpy := 0.5, 5.0, false
		if math.Hypot(d.X, d.Y) > 1.6*dt+0.3 || math.Abs(d.Th) > 1.2*dt+0.2 {
			// the robot's own pose jumped (relocalisation): ignore it, assume steady motion, search wider
			jumpy = true
			guess = poses[k-1]
			rxy, rth = 1.8, 40.0
			if k >= 2 {
				guess = compose(poses[k-1], rel(poses[k-2], poses[k-1]))
			}
		}
		g.clear()
		used := 0
		for q := len(kfs) - 1; q >= 0 && used < 120; q-- {
			if math.Hypot(kfs[q].pose.X-guess.X, kfs[q].pose.Y-guess.Y) > 12 {
				continue
			}
			used++
			for _, p := range kfs[q].rp {
				g.add(tf(p, kfs[q].pose), true)
			}
		}
		var sc, alt int
		poses[k], sc, alt = chainSearch(g, s.RP, guess, rxy, rth)
		if 50*float64(sc)/float64(len(s.RP)) < BreakFit && used > 0 {
			// poor match: look wider once (samples may be missing, or the robot was moved)
			if p2, s2, a2 := chainSearch(g, s.RP, guess, 1.8, 20); s2 > sc {
				poses[k], sc, alt = p2, s2, a2
			}
			if 50*float64(sc)/float64(len(s.RP)) < BreakFit {
				brk[k] = true // still poor: the track is not connected across this scan
				poses[k] = guess
				kfs = kfs[:0]
			}
		}
		lastScore, lastAlt = sc, alt
		mk(k, sc, alt, jumpy)
		if len(kfs) == 0 {
			kfs = append(kfs, kf{poses[k], s.RP})
		}
		last := kfs[len(kfs)-1].pose
		if math.Hypot(poses[k].X-last.X, poses[k].Y-last.Y) > 0.25 || math.Abs(wrap(poses[k].Th-last.Th)) > 0.17 {
			kfs = append(kfs, kf{poses[k], s.RP})
		}
	}
	if o.Progress != nil {
		o.Progress(len(ss), len(ss))
	}
	return out
}

type chainFile struct {
	Version int         `json:"version"`
	N       int         `json:"samples"`
	From    int         `json:"from"`
	To      int         `json:"to"`
	Poses   []ChainPose `json:"poses"`
}

const chainVersion = 3

func chainPath(dir string) string { return filepath.Join(dir, "_laser_track.json") }

// LoadChain returns the saved laser track of a drive, if it matches the recording and range.
func LoadChain(dir string, from, to int) ([]ChainPose, bool) {
	b, err := os.ReadFile(chainPath(dir))
	if err != nil {
		return nil, false
	}
	var f chainFile
	if json.Unmarshal(b, &f) != nil || f.Version != chainVersion || f.N != Count(dir) || f.From != from || f.To != to || len(f.Poses) < 2 {
		return nil, false
	}
	return f.Poses, true
}

// SaveChain stores the laser track next to the recording so it is computed once.
func SaveChain(dir string, from, to int, poses []ChainPose) error {
	b, _ := json.Marshal(chainFile{Version: chainVersion, N: Count(dir), From: from, To: to, Poses: poses})
	return os.WriteFile(chainPath(dir), b, 0644)
}

// TrackLength is the distance driven along the laser track.
func TrackLength(ch []ChainPose) float64 {
	l := 0.0
	for i := 1; i < len(ch); i++ {
		l += math.Hypot(ch[i].X-ch[i-1].X, ch[i].Y-ch[i-1].Y)
	}
	return l
}
