package lasertrack

import (
	"fmt"
	"math"
	"strings"

	"drishtimap/smap"
)

// Range is a span of sample numbers, inclusive.
type Range struct{ From, To int }

func inRanges(i int, rs []Range) bool {
	for _, r := range rs {
		if i >= r.From && i <= r.To {
			return true
		}
	}
	return len(rs) == 0
}

// Align is a rigid fit "laser track -> map track" over some scans, with its quality.
type Align struct {
	T      smap.Rigid `json:"t"`
	RotDeg float64    `json:"rotDeg"`
	RMS    float64    `json:"rms"`
	Max    float64    `json:"max"`
	N      int        `json:"n"`
	Ranges []Range    `json:"ranges"`
}

// FitRigid finds the rotation + shift that best maps p[i] onto q[i] (least squares).
func FitRigid(p, q []smap.Pt) (smap.Rigid, float64, float64) {
	n := float64(len(p))
	var mx, my, MX, MY float64
	for i := range p {
		mx += p[i].X
		my += p[i].Y
		MX += q[i].X
		MY += q[i].Y
	}
	mx, my, MX, MY = mx/n, my/n, MX/n, MY/n
	var A, B float64
	for i := range p {
		px, py, qx, qy := p[i].X-mx, p[i].Y-my, q[i].X-MX, q[i].Y-MY
		A += px*qx + py*qy
		B += px*qy - py*qx
	}
	th := math.Atan2(B, A)
	c, s := math.Cos(th), math.Sin(th)
	t := smap.Rigid{Th: th, Tx: MX - (c*mx - s*my), Ty: MY - (s*mx + c*my)}
	var e, me float64
	for i := range p {
		r := t.Apply(p[i])
		d := math.Hypot(r.X-q[i].X, r.Y-q[i].Y)
		e += d * d
		me = math.Max(me, d)
	}
	return t, math.Sqrt(e / n), me
}

// AlignTrack fits the laser track to the track a map gives (a tracking replay), over the given
// sample ranges, using only scans whose wall match is at least minFit %.
func AlignTrack(ch []ChainPose, rows []Row, rs []Range, minFit float64) (*Align, error) {
	cp := map[int]ChainPose{}
	for _, c := range ch {
		cp[c.I] = c
	}
	var p, q []smap.Pt
	for _, r := range rows {
		c, ok := cp[r.I]
		if !ok || !inRanges(r.I, rs) || r.Fit < minFit {
			continue
		}
		p = append(p, smap.Pt{X: c.X, Y: c.Y})
		q = append(q, smap.Pt{X: r.X, Y: r.Y})
	}
	if len(p) < 8 {
		return nil, fmt.Errorf("only %d usable scans in that range (need 8 with wall match >= %.0f%%)", len(p), minFit)
	}
	t, rms, mx := FitRigid(p, q)
	return &Align{T: t, RotDeg: t.Th * 180 / math.Pi, RMS: rms, Max: mx, N: len(p), Ranges: rs}, nil
}

// Turn is the correction for a piece of map that is attached at the wrong angle.
type Turn struct {
	T        smap.Rigid `json:"t"` // apply to the piece (its current map coordinates) to put it in the reference frame
	RotDeg   float64    `json:"rotDeg"`
	Pivot    *smap.Pt   `json:"pivot,omitempty"`
	Piece    *Align     `json:"piece"`
	Ref      *Align     `json:"reference"`
	Stations []StMove   `json:"stations"`
}

// StMove is how far the correction moves a station.
type StMove struct {
	Name   string `json:"name"`
	X, Y   float64
	DX, DY float64
	D      float64 `json:"d"`
}

// MeasureTurn: piece = fit of the laser track to the part of the map that is in the wrong place;
// ref = fit of the same laser track to the part (or map) that is right. The correction is
// "undo the piece fit, then apply the reference fit".
func MeasureTurn(piece, ref *Align) *Turn {
	t := piece.T.Inverse().Then(ref.T)
	tr := &Turn{T: t, RotDeg: t.Th * 180 / math.Pi, Piece: piece, Ref: ref}
	if pv, ok := t.Pivot(); ok && math.Abs(pv.X) < 1e5 && math.Abs(pv.Y) < 1e5 {
		tr.Pivot = &pv
	}
	return tr
}

// Moves lists how far the correction would move each station inside the polygon (all if poly is empty).
func (t *Turn) Moves(m *smap.Map, poly []smap.Pt) []StMove {
	var out []StMove
	for _, s := range m.Stations() {
		if len(poly) >= 3 && !smap.InPoly(s.Pos, poly) {
			continue
		}
		n := t.T.Apply(s.Pos)
		out = append(out, StMove{Name: s.Name, X: s.Pos.X, Y: s.Pos.Y, DX: n.X - s.Pos.X, DY: n.Y - s.Pos.Y, D: math.Hypot(n.X-s.Pos.X, n.Y-s.Pos.Y)})
	}
	return out
}

func (t *Turn) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "piece fit:     rotation %.3f deg, rms %.3f m, worst %.2f m, %d scans\n", t.Piece.RotDeg, t.Piece.RMS, t.Piece.Max, t.Piece.N)
	fmt.Fprintf(&b, "reference fit: rotation %.3f deg, rms %.3f m, worst %.2f m, %d scans\n", t.Ref.RotDeg, t.Ref.RMS, t.Ref.Max, t.Ref.N)
	fmt.Fprintf(&b, "correction for the piece: turn %.3f deg", t.RotDeg)
	if t.Pivot != nil {
		fmt.Fprintf(&b, " about (%.2f, %.2f)", t.Pivot.X, t.Pivot.Y)
	}
	fmt.Fprintf(&b, "   [th %.6f rad, tx %.4f, ty %.4f]\n", t.T.Th, t.T.Tx, t.T.Ty)
	if t.Piece.RMS > 0.15 || t.Ref.RMS > 0.15 {
		b.WriteString("WARNING: a fit above 0.15 m rms means that range is not one rigid piece; narrow the scan range.\n")
	}
	return b.String()
}

// DriftRow is "where the map puts the robot" minus "where the rigid laser track puts it".
type DriftRow struct {
	I        int     `json:"i"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	DX       float64 `json:"dx"`
	DY       float64 `json:"dy"`
	D        float64 `json:"d"`
	DHeading float64 `json:"dDeg"`
	Fit      float64 `json:"fit"`
	Near     string  `json:"near"`
}

// Drift aligns the laser track to the map track on the reference ranges (a part of the map that is
// trusted) and reports how far the map track departs from it everywhere else. A difference that grows
// steadily with distance from one point is a piece of map attached at the wrong angle.
func Drift(ch []ChainPose, rows []Row, ref []Range, minFit float64) ([]DriftRow, *Align, error) {
	al, err := AlignTrack(ch, rows, ref, minFit)
	if err != nil {
		return nil, nil, err
	}
	cp := map[int]ChainPose{}
	for _, c := range ch {
		cp[c.I] = c
	}
	var out []DriftRow
	for _, r := range rows {
		c, ok := cp[r.I]
		if !ok {
			continue
		}
		p := al.T.Apply(smap.Pt{X: c.X, Y: c.Y})
		dh := r.Deg - (c.Th+al.T.Th)*180/math.Pi
		for dh > 180 {
			dh -= 360
		}
		for dh < -180 {
			dh += 360
		}
		out = append(out, DriftRow{I: r.I, X: r.X, Y: r.Y, DX: r.X - p.X, DY: r.Y - p.Y, D: math.Hypot(r.X-p.X, r.Y-p.Y), DHeading: dh, Fit: r.Fit, Near: r.Near})
	}
	return out, al, nil
}

// DriftText prints every n-th row plus the maximum.
func DriftText(rows []DriftRow, al *Align, every int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "laser track aligned to the map on %d scans (rms %.3f m, worst %.2f m)\n", al.N, al.RMS, al.Max)
	b.WriteString("scan   map x      y   map-minus-track dx    dy   dist  heading  match  near\n")
	var worst DriftRow
	for k, r := range rows {
		if r.D > worst.D && r.Fit >= 40 {
			worst = r
		}
		if every > 0 && k%every == 0 {
			fmt.Fprintf(&b, "%4d %7.1f %6.1f              %+6.2f %+6.2f  %5.2f  %+5.1f   %3.0f%%  %s\n", r.I, r.X, r.Y, r.DX, r.DY, r.D, r.DHeading, r.Fit, r.Near)
		}
	}
	fmt.Fprintf(&b, "largest difference: %.2f m at scan %d near %s (%.1f, %.1f)\n", worst.D, worst.I, worst.Near, worst.X, worst.Y)
	return b.String()
}

// Piece is a stretch of the drive over which the map is rigid against the lasers.
type Piece struct {
	Range  Range   `json:"range"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Align  *Align  `json:"align"`
	Length float64 `json:"length"` // metres driven inside the piece
	// relation to the previous piece
	StepAtSeam float64 `json:"stepAtSeam"` // metres the two fits disagree where they meet
	TurnDeg    float64 `json:"turnDeg"`    // rotation of this piece against the previous one
}

// Pieces cuts a drive into stretches where the map agrees with the rigid laser track, and reports
// how each stretch sits against the one before it. Input is a tracking replay through the map.
// tol is the residual (metres) at which a stretch is cut, 0.30 by default.
func Pieces(ch []ChainPose, rows []Row, minFit, tol float64) []Piece {
	if tol <= 0 {
		tol = 0.30
	}
	cp := map[int]ChainPose{}
	for _, c := range ch {
		cp[c.I] = c
	}
	type pr struct {
		i    int
		p, q smap.Pt
		near string
	}
	var all []pr
	for _, r := range rows {
		if c, ok := cp[r.I]; ok && r.Fit >= minFit {
			all = append(all, pr{r.I, smap.Pt{X: c.X, Y: c.Y}, smap.Pt{X: r.X, Y: r.Y}, r.Near})
		}
	}
	fit := func(a, b int) (smap.Rigid, float64, float64) {
		p, q := make([]smap.Pt, 0, b-a), make([]smap.Pt, 0, b-a)
		for _, e := range all[a:b] {
			p, q = append(p, e.p), append(q, e.q)
		}
		return FitRigid(p, q)
	}
	const seed, look = 12, 6
	var out []Piece
	a := 0
	for a+seed <= len(all) {
		b := a + seed
		t, _, _ := fit(a, b)
		for b < len(all) {
			// do the next scans still agree with the fit so far?
			bad, n := 0, 0
			for k := b; k < len(all) && k < b+look; k++ {
				r := t.Apply(all[k].p)
				n++
				if math.Hypot(r.X-all[k].q.X, r.Y-all[k].q.Y) > tol {
					bad++
				}
			}
			if bad == n {
				break
			}
			b++
			if (b-a)%4 == 0 {
				t2, _, mx := fit(a, b)
				if mx > 1.6*tol {
					b--
					break
				}
				t = t2
			}
		}
		t, rms, mx := fit(a, b)
		ln := 0.0
		for k := a + 1; k < b; k++ {
			ln += math.Hypot(all[k].p.X-all[k-1].p.X, all[k].p.Y-all[k-1].p.Y)
		}
		pc := Piece{Range: Range{all[a].i, all[b-1].i}, From: all[a].near, To: all[b-1].near, Length: ln,
			Align: &Align{T: t, RotDeg: t.Th * 180 / math.Pi, RMS: rms, Max: mx, N: b - a}}
		if len(out) > 0 {
			pv := out[len(out)-1]
			s1, s2 := pv.Align.T.Apply(all[a].p), t.Apply(all[a].p)
			pc.StepAtSeam = math.Hypot(s1.X-s2.X, s1.Y-s2.Y)
			pc.TurnDeg = wrap(t.Th-pv.Align.T.Th) * 180 / math.Pi
		}
		out = append(out, pc)
		a = b
	}
	return out
}

// PiecesText renders the piece list.
func PiecesText(ps []Piece) string {
	var b strings.Builder
	b.WriteString("stretches where the map is rigid against the lasers:\n")
	for i, p := range ps {
		fmt.Fprintf(&b, " %2d. scans %4d-%4d  %-6s -> %-6s  %5.1f m driven, fit rms %.2f m (worst %.2f)", i+1, p.Range.From, p.Range.To, p.From, p.To, p.Length, p.Align.RMS, p.Align.Max)
		if i > 0 {
			fmt.Fprintf(&b, "   vs previous: step %.2f m at the seam, turned %+.2f deg", p.StepAtSeam, p.TurnDeg)
		}
		b.WriteString("\n")
	}
	b.WriteString("A step of 0.5 m or more at a seam is where a robot jumps. A turn between two long stretches means one of them is attached at the wrong angle.\n")
	return b.String()
}
