package app

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"drishtimap/lasertrack"
	"drishtimap/seer"
	"drishtimap/smap"
)

const armMargin = 12.0 // metres around the box in which the recording runs (run-in and run-out)

// recArm waits (reading only the robot's position) until the robot comes near the box, records
// while it is there, and stops by itself once it has left. Nobody has to time a start and a stop,
// and the robot is sent the usual way, by the fleet manager: this program never moves a robot.
func (a *App) recArm(robot, name string, box [4]float64, minutes int) (*Result, error) {
	ip, err := a.robotIP(robot)
	if err != nil {
		return nil, err
	}
	n, err := safeName(name)
	if err != nil {
		return nil, err
	}
	if minutes <= 0 || minutes > 240 {
		minutes = 120
	}
	a.mu.Lock()
	if a.rec != nil {
		select {
		case <-a.rec.done:
		default:
			a.mu.Unlock()
			return nil, fmt.Errorf("a recording is already running or armed (%s on %s); stop it first", a.rec.name, a.rec.robot)
		}
	}
	a.mu.Unlock()
	dir := a.ws("drives", n)
	if lasertrack.Count(dir) > 0 {
		return nil, fmt.Errorf("a recording named %q already exists; choose another name", n)
	}
	ctx0, c0 := context.WithTimeout(context.Background(), 8*time.Second)
	_, err = seer.ReadStatus(ctx0, ip)
	c0()
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s (%s): %v", robot, ip, err)
	}
	near := func(x, y float64) bool {
		return x >= box[0]-armMargin && x <= box[2]+armMargin && y >= box[1]-armMargin && y <= box[3]+armMargin
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	r := &recording{robot: robot, name: n, cancel: cancel, done: make(chan struct{}), started: time.Now(), phase: "armed: waiting for the robot to come near the box"}
	a.mu.Lock()
	a.rec = r
	a.mu.Unlock()
	set := func(f func()) { a.mu.Lock(); f(); a.mu.Unlock() }
	go func() {
		defer close(r.done)
		defer cancel()
		// 1. wait, reading only the position every 2 s
		for {
			c1, cc := context.WithTimeout(ctx, 6*time.Second)
			st, err := seer.ReadStatus(c1, ip)
			cc()
			if ctx.Err() != nil {
				set(func() { r.phase = "stopped before the robot reached the box; nothing was recorded" })
				return
			}
			if err == nil {
				set(func() {
					r.stats.X, r.stats.Y, r.stats.Conf, r.stats.Station = st.X, st.Y, st.Confidence, st.CurrentStation
				})
				if near(st.X, st.Y) {
					break
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
		// 2. record while it is near; stop 15 s after it has left
		set(func() { r.phase = "recording: the robot is at the box" })
		rctx, rcancel := context.WithCancel(ctx)
		defer rcancel()
		var away time.Time
		entered := false
		st, err := seer.Record(rctx, ip, dir, time.Duration(minutes)*time.Minute, 0, func(s seer.RecStats) {
			a.mu.Lock()
			r.stats = s
			a.mu.Unlock()
			if s.X >= box[0] && s.X <= box[2] && s.Y >= box[1] && s.Y <= box[3] {
				entered = true
			}
			switch {
			case near(s.X, s.Y):
				away = time.Time{}
			case away.IsZero():
				away = time.Now()
			case time.Since(away) > 15*time.Second:
				rcancel()
			}
		})
		set(func() {
			r.stats.Samples, r.stats.Failed = st.Samples, st.Failed
			if err != nil && rctx.Err() == nil {
				r.err = err
			}
			if entered {
				r.phase = "done: the robot drove through the box and has left"
			} else {
				r.phase = "done, but the robot passed near the box without driving through it"
			}
		})
	}()
	return &Result{Summary: fmt.Sprintf("armed: %s will be recorded as %q when it comes within %.0f m of the box, and the recording stops by itself when it has left (read-only; gives up after %d minutes). Send the robot the usual way.", robot, n, armMargin, minutes), Data: map[string]any{"drive": n}}, nil
}

// boxSources says what can be used as evidence for a box: which recordings drive through it, and
// which map versions really differ from the map in question inside it.
func (a *App) boxSources(c *Ctx, box [4]float64, mapName string) (*Result, error) {
	type drv struct {
		Name    string  `json:"name"`
		Samples int     `json:"samples"`
		Passes  int     `json:"passes"`
		Dir     string  `json:"dir"`
		Min     float64 `json:"min"`
		When    string  `json:"when"`
	}
	var drives []drv
	ds := a.listDrives()
	for i, d := range ds {
		c.progress(i, len(ds)+1, "looking at the recordings")
		row := drv{Name: d.Name, Samples: d.Samples, When: d.Modified, Min: 2}
		if sm, err := lasertrack.Summarize(a.ws("drives", d.Name), 0, 0); err == nil {
			inside := false
			var ex, ey float64
			dirs := map[string]bool{}
			for _, p := range sm.Track {
				here := inBox(smap.Pt{X: p.X, Y: p.Y}, box)
				if here && !inside {
					row.Passes++
					ex, ey = p.X, p.Y
				}
				if !here && inside {
					dirs[compass(p.X-ex, p.Y-ey)] = true
				}
				inside = here
				if here && p.M == 0 {
					row.Min = math.Min(row.Min, p.Conf)
				}
			}
			var dd []string
			for k := range dirs {
				if k != "" {
					dd = append(dd, k)
				}
			}
			sort.Strings(dd)
			row.Dir = strings.Join(dd, ", ")
		}
		if row.Min > 1 {
			row.Min = 0
		}
		drives = append(drives, row)
	}
	sort.SliceStable(drives, func(i, j int) bool { return drives[i].Passes > 0 && drives[j].Passes == 0 })

	// map versions: the same walls inside the box = the same version as far as this box goes
	type ver struct {
		Name   string `json:"name"`
		Same   bool   `json:"same"`   // same walls in the box as the map in question
		SameAs string `json:"sameAs"` // an earlier listed version with the same walls in the box
		Date   string `json:"date"`
		Walls  int    `json:"walls"`
		Pick   bool   `json:"pick"`
	}
	sig := func(name string) (string, int, error) {
		m, err := a.Map(name)
		if err != nil {
			return "", 0, err
		}
		var ks []string
		for _, p := range m.Walls() {
			if inBox(p, box) {
				ks = append(ks, fmt.Sprintf("%.2f,%.2f", p.X, p.Y))
			}
		}
		sort.Strings(ks)
		h := md5.Sum([]byte(strings.Join(ks, ";")))
		return hex.EncodeToString(h[:]), len(ks), nil
	}
	cur, _, err := sig(mapName)
	if err != nil {
		return nil, err
	}
	var vers []ver
	seen := map[string]string{}
	all := append(a.listMaps("maps"), a.listMaps("candidates")...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Modified > all[j].Modified })
	picked := 0
	for i, f := range all {
		c.progress(len(ds)+i, len(ds)+len(all), "comparing the map versions in the box")
		if f.Name == mapName {
			continue
		}
		s, n, err := sig(f.Name)
		if err != nil {
			continue
		}
		v := ver{Name: f.Name, Date: f.Modified, Walls: n, Same: s == cur, SameAs: seen[s]}
		if !v.Same && v.SameAs == "" {
			seen[s] = f.Name
			if picked < 3 && n > 0 {
				v.Pick = true
				picked++
			}
		}
		vers = append(vers, v)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "EVIDENCE SOURCES for the box  x %.1f..%.1f  y %.1f..%.1f\n\nRecordings\n", box[0], box[2], box[1], box[3])
	for _, d := range drives {
		if d.Passes > 0 {
			fmt.Fprintf(&b, "  %-32s %d pass(es) through the box, heading %s, lowest confidence %.2f\n", d.Name, d.Passes, d.Dir, d.Min)
		} else {
			fmt.Fprintf(&b, "  %-32s does not drive through the box\n", d.Name)
		}
	}
	if len(drives) == 0 {
		b.WriteString("  none yet\n")
	}
	fmt.Fprintf(&b, "\nMap versions, compared with %s inside the box\n", mapName)
	for _, v := range vers {
		switch {
		case v.Same:
			fmt.Fprintf(&b, "  %-40s same walls here: nothing to compare\n", v.Name)
		case v.SameAs != "":
			fmt.Fprintf(&b, "  %-40s same walls here as %s\n", v.Name, v.SameAs)
		case v.Walls == 0:
			fmt.Fprintf(&b, "  %-40s has no walls here (this area was not scanned in it)\n", v.Name)
		default:
			fmt.Fprintf(&b, "  %-40s DIFFERENT walls here (%d wall dots)\n", v.Name, v.Walls)
		}
	}
	return &Result{Summary: b.String(), Data: map[string]any{"drives": drives, "versions": vers}}, nil
}

func compass(dx, dy float64) string {
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

func box4(b []float64) ([4]float64, error) {
	if len(b) != 4 {
		return [4]float64{}, fmt.Errorf("give the box as x0,y0,x1,y1")
	}
	return [4]float64{min(b[0], b[2]), min(b[1], b[3]), max(b[0], b[2]), max(b[1], b[3])}, nil
}

func init() {
	reg(&Tool{Name: "record_arm", Desc: "READ-ONLY: arm a recording for one robot and a box. The program waits (reading only the robot's position) until that robot comes near the box, records position and lasers while it is there, and stops by itself when it has left. Use it instead of record_start when the aim is laser evidence for one place: nobody has to time the start and stop. The robot is sent by the fleet manager as usual; this program never moves a robot. record_status shows the phase; record_stop cancels.",
		Params: obj(`"robot":{"type":"string"},"name":{"type":"string","description":"name for the new recording"},"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4},"minutes":{"type":"integer","description":"give up after this many minutes (default 120)"}`, "robot", "name", "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Robot, Name string
				Box         []float64
				Minutes     int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			b, err := box4(p.Box)
			if err != nil {
				return nil, err
			}
			return c.App.recArm(p.Robot, p.Name, b, p.Minutes)
		}})
	reg(&Tool{Name: "box_sources", Desc: "What can serve as laser evidence for a box: which recordings in the workspace drive through it (passes, direction, lowest confidence), and which map versions have different walls inside it from the map in question (versions with the same walls there are grouped). Run it before box_evidence to choose recordings and versions.",
		Params: obj(pMap+`,"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4}`, "map", "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Map string
				Box []float64
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			b, err := box4(p.Box)
			if err != nil {
				return nil, err
			}
			return c.App.boxSources(c, b, p.Map)
		}})
}
