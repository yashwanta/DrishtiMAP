// Package lasertrack works on recorded drives: pose + laser samples read from a robot.
// It rebuilds the rigid track of a drive from the lasers alone (no map), replays the lasers
// through any map file, and measures how a map is bent against that rigid track.
package lasertrack

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"drishtimap/smap"
)

// Pose is a position and heading (radians).
type Pose struct{ X, Y, Th float64 }

// Loc is the robot's own localisation answer (status API 1004).
type Loc struct {
	X, Y, Angle, Confidence float64
	CurrentStation          string `json:"current_station"`
	LastStation             string `json:"last_station"`
	CreateOn                string `json:"create_on"`
	LocState                int    `json:"loc_state"`
	UpdateReason            int    `json:"update_reason"`
	LocMethod               int    `json:"loc_method"`
}

type laserMsg struct {
	CreateOn string `json:"create_on"`
	Lasers   []struct {
		Beams []struct {
			Angle, Dist float64
			Valid       bool
		}
		Install struct {
			Upside    bool
			X, Y, Yaw float64
		} `json:"install_info"`
	}
}

// Sample is one recorded moment: the laser points in the robot frame and the pose the robot believed.
type Sample struct {
	I       int
	RP      []smap.Pt
	Bel     Pose
	Conf    float64
	Station string
	T       float64
}

var locRe = regexp.MustCompile(`^loc_(\d+)\.json$`)

// Count returns the highest sample number in a drive folder.
func Count(dir string) int {
	es, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if m := locRe.FindStringSubmatch(e.Name()); m != nil {
			if k, _ := strconv.Atoi(m[1]); k > n {
				n = k
			}
		}
	}
	return n
}

func fname(dir, kind string, i int) string {
	return filepath.Join(dir, fmt.Sprintf("%s_%04d.json", kind, i))
}

// ReadLoc reads the pose of sample i.
func ReadLoc(dir string, i int) (Loc, error) {
	var l Loc
	b, err := os.ReadFile(fname(dir, "loc", i))
	if err != nil {
		return l, err
	}
	return l, json.Unmarshal(b, &l)
}

func wrap(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a < -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

// ParseTime reads the robot's timestamp format; 0 when it cannot be read.
func ParseTime(s string) float64 {
	t, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
	if err != nil {
		return 0
	}
	return float64(t.UnixMilli()) / 1000
}

func robotPts(las *laserMsg) []smap.Pt {
	var o []smap.Pt
	for _, l := range las.Lasers {
		for _, bm := range l.Beams {
			if !bm.Valid || bm.Dist < 0.3 || bm.Dist > 30 {
				continue
			}
			a := bm.Angle
			if !l.Install.Upside {
				a = -a
			}
			t := (a + l.Install.Yaw) * math.Pi / 180
			o = append(o, smap.Pt{X: l.Install.X + bm.Dist*math.Cos(t), Y: l.Install.Y + bm.Dist*math.Sin(t)})
		}
	}
	return o
}

// LoadSamples reads samples from..to (inclusive; to<=0 means the end). Samples with fewer than
// 150 valid laser points are skipped, as they cannot be matched.
func LoadSamples(dir string, from, to int) ([]*Sample, error) {
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
	out := make([]*Sample, to-from+1)
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i := from; i <= to; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			loc, e1 := ReadLoc(dir, i)
			b2, e2 := os.ReadFile(fname(dir, "laser", i))
			if e1 != nil || e2 != nil {
				return
			}
			var las laserMsg
			if json.Unmarshal(b2, &las) != nil {
				return
			}
			locb := loc
			if b3, e3 := os.ReadFile(fname(dir, "locb", i)); e3 == nil {
				json.Unmarshal(b3, &locb)
			}
			rp := robotPts(&las)
			if len(rp) < 150 {
				return
			}
			// the laser was read between the two pose reads: interpolate the believed pose to the laser time
			t1, tl, t2 := ParseTime(loc.CreateOn), ParseTime(las.CreateOn), ParseTime(locb.CreateOn)
			f := 0.5
			if t2 > t1 && tl >= t1 && tl <= t2 {
				f = (tl - t1) / (t2 - t1)
			}
			out[i-from] = &Sample{I: i, RP: rp, Conf: loc.Confidence, Station: loc.CurrentStation, T: tl,
				Bel: Pose{loc.X + f*(locb.X-loc.X), loc.Y + f*(locb.Y-loc.Y), loc.Angle + f*wrap(locb.Angle-loc.Angle)}}
		}(i)
	}
	wg.Wait()
	ss := out[:0]
	for _, s := range out {
		if s != nil {
			ss = append(ss, s)
		}
	}
	if len(ss) < 2 {
		return nil, fmt.Errorf("recording %s has no usable laser samples", filepath.Base(dir))
	}
	return ss, nil
}

func tf(p smap.Pt, q Pose) smap.Pt {
	c, s := math.Cos(q.Th), math.Sin(q.Th)
	return smap.Pt{X: q.X + c*p.X - s*p.Y, Y: q.Y + s*p.X + c*p.Y}
}

func compose(a, d Pose) Pose { // a, then d given in a's frame
	p := tf(smap.Pt{X: d.X, Y: d.Y}, a)
	return Pose{p.X, p.Y, a.Th + d.Th}
}

func rel(a, b Pose) Pose { // b seen from a
	dx, dy := b.X-a.X, b.Y-a.Y
	c, s := math.Cos(a.Th), math.Sin(a.Th)
	return Pose{c*dx + s*dy, -s*dx + c*dy, wrap(b.Th - a.Th)}
}

// grid is a 5 cm occupancy grid: 2 on a wall cell, 1 on its 8 neighbours.
type grid struct {
	x0, y0  float64 // centre of cell (0,0)
	w, h    int
	v       []uint8
	touched []int
}

const cellSize = 0.05

// GridShift moves the grid origin; tests use it to prove results do not depend on where the grid sits.
var GridShift = 0.0

func newGrid(minX, minY, maxX, maxY float64) *grid {
	// cells are centred on multiples of 5 cm: map wall dots lie on that lattice, so they fall in the
	// middle of a cell and not on an edge where rounding would decide the cell
	g := &grid{x0: math.Floor(minX) + GridShift, y0: math.Floor(minY) + GridShift}
	g.w = int(math.Ceil((maxX-g.x0)/cellSize)) + 2
	g.h = int(math.Ceil((maxY-g.y0)/cellSize)) + 2
	g.v = make([]uint8, g.w*g.h)
	return g
}

func (g *grid) add(p smap.Pt, track bool) {
	ix, iy := int(math.Floor((p.X-g.x0)/cellSize+0.5)), int(math.Floor((p.Y-g.y0)/cellSize+0.5))
	for a := -1; a <= 1; a++ {
		for b := -1; b <= 1; b++ {
			x, y := ix+a, iy+b
			if x < 0 || y < 0 || x >= g.w || y >= g.h {
				continue
			}
			var val uint8 = 1
			if a == 0 && b == 0 {
				val = 2
			}
			k := y*g.w + x
			if track && g.v[k] == 0 {
				g.touched = append(g.touched, k)
			}
			if g.v[k] < val {
				g.v[k] = val
			}
		}
	}
}

func (g *grid) clear() {
	for _, k := range g.touched {
		g.v[k] = 0
	}
	g.touched = g.touched[:0]
}

func (g *grid) at(x, y float64) int {
	ix, iy := int(math.Floor((x-g.x0)/cellSize+0.5)), int(math.Floor((y-g.y0)/cellSize+0.5))
	if ix < 0 || iy < 0 || ix >= g.w || iy >= g.h {
		return 0
	}
	return int(g.v[iy*g.w+ix])
}

// score sums the grid values under the laser points placed at pose q; on = points that touch a wall.
func (g *grid) score(rp []smap.Pt, q Pose, step int) (sc, on int) {
	c, s := math.Cos(q.Th), math.Sin(q.Th)
	for k := 0; k < len(rp); k += step {
		p := rp[k]
		v := g.at(q.X+c*p.X-s*p.Y, q.Y+s*p.X+c*p.Y)
		sc += v
		if v > 0 {
			on++
		}
	}
	return
}

// steps lists -r, -r+s, ..., r exactly as the original scripts stepped through a search window.
func steps(r, s float64) []float64 {
	var o []float64
	for d := -r; d <= r+1e-9; d += s {
		o = append(o, d)
	}
	return o
}

// parallelDo runs f(0..n-1) on all cores.
func parallelDo(n int, f func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); f(i) }(i)
	}
	wg.Wait()
}
