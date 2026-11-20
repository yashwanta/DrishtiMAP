package app

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"drishtimap/lasertrack"
	"drishtimap/smap"
)

// A real laser reply from a recording, placed on the real map: starting 1.5 m and 4 degrees away
// from where the robot was, the fit must come back to the robot's own position.
func TestFitScanOnRealData(t *testing.T) {
	ws := os.Getenv("DRISHTI_WS")
	if ws == "" {
		t.Skip("DRISHTI_WS not set")
	}
	maps, _ := filepath.Glob(filepath.Join(ws, "maps", "*_live_421395c7.smap"))
	dir := filepath.Join(ws, "drives", "amr01_west_nov14")
	if len(maps) == 0 || lasertrack.Count(dir) == 0 {
		t.Skip("reference map or recording not in the workspace")
	}
	m, err := smap.Load(maps[0])
	if err != nil {
		t.Fatal(err)
	}
	wall := map[[2]int]bool{}
	for _, p := range m.Walls() {
		k := cellOf(p)
		for i := -1; i <= 1; i++ {
			for j := -1; j <= 1; j++ {
				wall[[2]int{k[0] + i, k[1] + j}] = true
			}
		}
	}
	tried, good := 0, 0
	for _, i := range []int{20, 60, 100, 140, 180, 220} {
		b, err := os.ReadFile(filepath.Join(dir, "laser_"+pad4(i)+".json"))
		if err != nil {
			continue
		}
		loc, err := lasertrack.ReadLoc(dir, i)
		if err != nil || loc.Confidence < 0.7 {
			continue
		}
		pts, lasers, err := lasertrack.ParseLaser(b)
		if err != nil || lasers == 0 || len(pts) < 150 {
			t.Fatalf("sample %d: %d lasers, %d points, %v", i, lasers, len(pts), err)
		}
		var near []smap.Pt
		for _, p := range pts {
			if p.X*p.X+p.Y*p.Y <= 15*15 {
				near = append(near, p)
			}
		}
		tried++
		x, y, th, match := fitScan(wall, near, loc.X+1.1, loc.Y-1.0, loc.Angle+4*math.Pi/180, 2.0)
		d := math.Hypot(x-loc.X, y-loc.Y)
		t.Logf("sample %d: robot at (%.2f, %.2f) confidence %.2f; fit %.2f m away, %.1f deg off, %.0f %% of points on walls", i, loc.X, loc.Y, loc.Confidence, d, (th-loc.Angle)*180/math.Pi, match)
		if d <= 0.35 && math.Abs(th-loc.Angle) <= 2.5*math.Pi/180 && match >= 40 {
			good++
		}
	}
	if tried < 3 {
		t.Skip("too few usable samples")
	}
	if good < tried-1 {
		t.Errorf("only %d of %d scans were placed back at the robot's own position", good, tried)
	}
}

func pad4(i int) string {
	s := "0000" + itoa(i)
	return s[len(s)-4:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
