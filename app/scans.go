package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"drishtimap/fleet"
	"drishtimap/lasertrack"
	"drishtimap/smap"
)

// Laser scans that the plant's AMR Health server collected by itself are read from that server.
// The server is the one named in the workspace's History sources; the sign-in is asked once and
// kept encrypted for this Windows user, like the AI keys. Nothing but the sign-in and the questions
// "which scans" and "give me scan N" is sent to it.

// HealthLogin is a stored AMR Health sign-in for one server address.
type HealthLogin struct {
	User    string `json:"user"`
	PassEnc string `json:"passEnc"`
}

// SaveHealthLogin checks a sign-in against the workspace's AMR Health server and stores it.
func (a *App) SaveHealthLogin(c *Ctx, user, pass string) error {
	site := a.Site()
	if site.HealthURL == "" {
		return fmt.Errorf("no AMR Health server is set for this workspace; enter its address in History sources first")
	}
	h := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}
	tok, err := h.Login(c, strings.TrimSpace(user), pass)
	if err != nil {
		return err
	}
	enc, err := protect(pass)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.Cfg.HealthLogins == nil {
		a.Cfg.HealthLogins = map[string]HealthLogin{}
	}
	a.Cfg.HealthLogins[site.HealthURL] = HealthLogin{User: strings.TrimSpace(user), PassEnc: enc}
	a.hTok, a.hTokURL, a.hTokAt = tok, site.HealthURL, time.Now()
	a.mu.Unlock()
	return a.saveCfg()
}

// HealthLoginUser says which user is stored for the workspace's AMR Health server ("" = none).
func (a *App) HealthLoginUser() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Cfg.HealthLogins[a.Site().HealthURL].User
}

func (a *App) healthToken(c *Ctx) (fleet.Health, string, error) {
	site := a.Site()
	if site.HealthURL == "" {
		return fleet.Health{}, "", fmt.Errorf("no AMR Health server is set for this workspace; enter its address in History sources")
	}
	h := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}
	a.mu.Lock()
	tok, fresh := a.hTok, a.hTokURL == site.HealthURL && time.Since(a.hTokAt) < 10*time.Hour
	lg, ok := a.Cfg.HealthLogins[site.HealthURL]
	a.mu.Unlock()
	if tok != "" && fresh {
		return h, tok, nil
	}
	if !ok || lg.User == "" {
		return h, "", fleet.ErrSignIn
	}
	pass, err := unprotect(lg.PassEnc)
	if err != nil {
		return h, "", fleet.ErrSignIn
	}
	tok, err = h.Login(c, lg.User, pass)
	if err != nil {
		return h, "", err
	}
	a.mu.Lock()
	a.hTok, a.hTokURL, a.hTokAt = tok, site.HealthURL, time.Now()
	a.mu.Unlock()
	return h, tok, nil
}

// scanAccess decides how to ask this server for scans: without a sign-in when the server allows
// that (remembered for this run), otherwise with the stored sign-in.
func (a *App) scanAccess(c *Ctx) (fleet.Health, string, error) {
	site := a.Site()
	if site.HealthURL == "" {
		return fleet.Health{}, "", fmt.Errorf("no AMR Health server is set for this workspace; enter its address in History sources")
	}
	h := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}
	a.mu.Lock()
	open, known := a.hOpen[site.HealthURL]
	a.mu.Unlock()
	if !known {
		_, err := h.Scans(c, "", "", time.Now().Add(-time.Minute), time.Now(), 1)
		open = err == nil
		if err != nil && !errors.Is(err, fleet.ErrSignIn) {
			return h, "", err
		}
		a.mu.Lock()
		if a.hOpen == nil {
			a.hOpen = map[string]bool{}
		}
		a.hOpen[site.HealthURL] = open
		a.mu.Unlock()
	}
	if open {
		return h, "", nil
	}
	return a.healthToken(c)
}

func (a *App) forgetHealthToken() { a.mu.Lock(); a.hTok = ""; a.mu.Unlock() }

// scanList reads the stored scans of a time range; near the box when one is given.
func (a *App) scanList(c *Ctx, box *[4]float64, from, to time.Time, robot string) ([]fleet.Scan, error) {
	// first without a sign-in: a server that offers its scans to the plant network needs none
	h, tok, err := a.scanAccess(c)
	if err != nil {
		return nil, err
	}
	scans, err := h.Scans(c, tok, robot, from, to, 5000)
	if errors.Is(err, fleet.ErrSignIn) && tok != "" {
		a.forgetHealthToken()
		if h, tok, err = a.healthToken(c); err == nil {
			scans, err = h.Scans(c, tok, robot, from, to, 5000)
		}
	}
	if err != nil {
		return nil, err
	}
	if box == nil {
		return scans, nil
	}
	var out []fleet.Scan
	for _, s := range scans {
		if s.X != nil && s.Y != nil && *s.X >= box[0]-armMargin && *s.X <= box[2]+armMargin && *s.Y >= box[1]-armMargin && *s.Y <= box[3]+armMargin {
			out = append(out, s)
		}
	}
	return out, nil
}

// scanPlace says where a scan was taken, in words.
func scanPlace(s fleet.Scan) string {
	if s.X == nil || s.Y == nil {
		return "an unknown position"
	}
	if s.Station != "" {
		return fmt.Sprintf("%s (%.1f, %.1f)", s.Station, *s.X, *s.Y)
	}
	return fmt.Sprintf("(%.1f, %.1f)", *s.X, *s.Y)
}

func scanUsable(s fleet.Scan) bool { return s.HasPoints && s.X != nil && s.Y != nil && s.Angle != nil }

func window(hours float64, fromS, toS string) (time.Time, time.Time, error) {
	to := time.Now()
	from := to.Add(-48 * time.Hour)
	if hours > 0 {
		from = to.Add(-time.Duration(hours * float64(time.Hour)))
	}
	if fromS != "" {
		var err error
		if from, err = parseWhen(fromS); err != nil {
			return from, to, err
		}
		if toS != "" {
			if to, err = parseWhen(toS); err != nil {
				return from, to, err
			}
		}
	}
	return from, to, nil
}

// fitScan places one scan on the map: it starts from the position the server stored and looks
// around it for the place where most laser points land on walls. The stored position was reported
// a moment before the scan, so a moving robot is not exactly there any more.
func fitScan(wall map[[2]int]bool, pts []smap.Pt, x, y, th, radius float64) (bx, by, bth, match float64) {
	sub := pts
	if len(sub) > 240 {
		step := len(sub) / 240
		var s []smap.Pt
		for i := 0; i < len(sub); i += step {
			s = append(s, sub[i])
		}
		sub = s
	}
	score := func(px, py, pt float64, ps []smap.Pt) int {
		cs, sn := math.Cos(pt), math.Sin(pt)
		n := 0
		for _, p := range ps {
			if wall[cellOf(smap.Pt{X: px + cs*p.X - sn*p.Y, Y: py + sn*p.X + cs*p.Y})] {
				n++
			}
		}
		return n
	}
	best := -1
	bx, by, bth = x, y, th
	search := func(cx, cy, ct, r, step, rt, stepT float64) {
		for dt := -rt; dt <= rt+1e-9; dt += stepT {
			for dx := -r; dx <= r+1e-9; dx += step {
				for dy := -r; dy <= r+1e-9; dy += step {
					if n := score(cx+dx, cy+dy, ct+dt, sub); n > best {
						best, bx, by, bth = n, cx+dx, cy+dy, ct+dt
					}
				}
			}
		}
	}
	deg := math.Pi / 180
	search(x, y, th, radius, 0.2, 8*deg, 2*deg)
	cx, cy, ct := bx, by, bth
	search(cx, cy, ct, 0.2, 0.05, 2*deg, 0.5*deg)
	if len(pts) == 0 {
		return bx, by, bth, 0
	}
	return bx, by, bth, 100 * float64(score(bx, by, bth, pts)) / float64(len(pts))
}

func (a *App) scanEvidence(c *Ctx, box [4]float64, mapName string, from, to time.Time, robots []string) (*Result, error) {
	m, err := a.Map(mapName)
	if err != nil {
		return nil, err
	}
	c.prog(0.02, "asking AMR Health which scans it has")
	list, err := a.scanList(c, &box, from, to, "")
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, r := range robots {
		want[r] = true
	}
	var use []fleet.Scan
	doubtful, noPoints := 0, 0
	for _, s := range list {
		if len(want) > 0 && !want[s.AMR] {
			continue
		}
		if !scanUsable(s) {
			noPoints++
			continue
		}
		use = append(use, s)
	}
	if len(use) > 300 {
		use = use[:300]
	}
	if len(use) == 0 {
		return nil, fmt.Errorf("AMR Health has no usable laser scan near this box between %s and %s (%d scans there had no points or no position). Scans are stored only for the robots named in its settings, at their sample times or when they have a problem", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), noPoints)
	}
	// walls as 10 cm cells, widened by one cell so that a point 15 cm from a wall dot still counts
	wall := map[[2]int]bool{}
	var inWalls []smap.Pt
	for _, p := range m.Walls() {
		k := cellOf(p)
		for i := -1; i <= 1; i++ {
			for j := -1; j <= 1; j++ {
				wall[[2]int{k[0] + i, k[1] + j}] = true
			}
		}
		if inBox(p, box) {
			inWalls = append(inWalls, p)
		}
	}
	h, tok, err := a.scanAccess(c)
	if err != nil {
		return nil, err
	}
	type placed struct {
		s           fleet.Scan
		x, y, match float64
		moved       float64
		cells       map[[2]int]bool
	}
	var ok []placed
	rejected := 0
	bestRejected, rejPts, rejConf := 0.0, 0, -1.0
	weak := 0
	for i, s := range use {
		c.progress(i, len(use), fmt.Sprintf("placing scan %d of %d on the map", i+1, len(use)))
		if c.Err() != nil {
			return nil, c.Err()
		}
		reply, err := h.ScanReply(c, tok, s.ID)
		if err != nil {
			return nil, err
		}
		pts, _, err := lasertrack.ParseLaser(reply)
		if err != nil || len(pts) < 100 {
			noPoints++
			continue
		}
		var near []smap.Pt
		for _, p := range pts {
			if p.X*p.X+p.Y*p.Y <= 15*15 {
				near = append(near, p)
			}
		}
		lag := math.Abs(s.CapturedAt.Sub(s.StatusAt).Seconds())
		radius := math.Min(4, 0.6+1.5*lag)
		fx, fy, fth, match := fitScan(wall, near, *s.X, *s.Y, *s.Angle, radius)
		if s.Confidence != nil && *s.Confidence < 0.5 {
			doubtful++
		}
		moved0 := math.Hypot(fx-*s.X, fy-*s.Y)
		if match < 40 && match >= 20 && moved0 <= 0.6 {
			weak++ // placed where the robot said it was; the lasers and the map agree poorly there
		} else if match < 40 {
			rejected++
			if match > bestRejected {
				bestRejected, rejPts, rejConf = match, len(near), -1
				if s.Confidence != nil {
					rejConf = *s.Confidence
				}
			}
			continue
		}
		p := placed{s: s, x: fx, y: fy, match: match, moved: math.Hypot(fx-*s.X, fy-*s.Y), cells: map[[2]int]bool{}}
		cs, sn := math.Cos(fth), math.Sin(fth)
		for _, q := range near {
			w := smap.Pt{X: fx + cs*q.X - sn*q.Y, Y: fy + sn*q.X + cs*q.Y}
			if inBox(w, box) {
				p.cells[cellOf(w)] = true
			}
		}
		ok = append(ok, p)
	}
	if len(ok) == 0 {
		return nil, fmt.Errorf("%d stored scan(s) were found near the box and read (%d laser points within 15 m in the best one), but none fits %s well enough to be placed: at best %.0f %% of the points land on walls, and 40 %% is needed. The robot itself reported confidence %.2f there, so the lasers and the map really do not agree at that spot: something stands there that is not in the map, or the walls there are not what was scanned. Open the scan viewer (Advanced, LiDAR, Live from robot) to see it", len(use), rejPts, mapName, bestRejected, rejConf)
	}
	// what the scans agree on inside the box
	hits := map[[2]int]int{}
	for _, p := range ok {
		for k := range p.cells {
			hits[k]++
		}
	}
	var las []smap.Pt
	for k := range hits {
		las = append(las, cellPt(k))
	}
	lasGrid := smap.NewNearGrid(las)
	wg := smap.NewNearGrid(inWalls)
	var seen, unseen, always, sometimes []smap.Pt
	for _, p := range inWalls {
		if lasGrid.Near(p) <= evTol {
			seen = append(seen, p)
		} else {
			unseen = append(unseen, p)
		}
	}
	for k, n := range hits {
		p := cellPt(k)
		if wg.Near(p) <= evTol {
			continue
		}
		could := 0
		for _, s := range ok {
			if math.Hypot(s.x-p.X, s.y-p.Y) <= 12 {
				could++
			}
		}
		if len(ok) == 1 || (could > 0 && float64(n) >= 0.6*float64(could)) {
			always = append(always, p)
		} else {
			sometimes = append(sometimes, p)
		}
	}
	// each robot against the map: a scanner that is off shows as one robot matching worse than the rest
	type rob struct {
		Robot string  `json:"robot"`
		Scans int     `json:"scans"`
		Match float64 `json:"match"`
		Moved float64 `json:"moved"`
		Odd   bool    `json:"odd"`
	}
	by := map[string][]placed{}
	for _, p := range ok {
		by[p.s.AMR] = append(by[p.s.AMR], p)
	}
	var robs []rob
	var meds []float64
	for name, ps := range by {
		var ms, mv []float64
		for _, p := range ps {
			ms, mv = append(ms, p.match), append(mv, p.moved)
		}
		sort.Float64s(ms)
		sort.Float64s(mv)
		robs = append(robs, rob{Robot: name, Scans: len(ps), Match: ms[len(ms)/2], Moved: mv[len(mv)/2]})
		meds = append(meds, ms[len(ms)/2])
	}
	sort.Slice(robs, func(i, j int) bool { return robs[i].Robot < robs[j].Robot })
	sort.Float64s(meds)
	fleetMed := meds[len(meds)/2]
	var odd []string
	for i := range robs {
		if len(robs) >= 3 && robs[i].Scans >= 3 && robs[i].Match < fleetMed-10 {
			robs[i].Odd = true
			odd = append(odd, robs[i].Robot)
		}
	}
	pct := func(n, of int) float64 {
		if of == 0 {
			return 0
		}
		return 100 * float64(n) / float64(of)
	}
	nw := len(seen) + len(unseen)
	var b strings.Builder
	fmt.Fprintf(&b, "STORED SCANS in the box  x %.1f..%.1f  y %.1f..%.1f,  %s to %s\n", box[0], box[2], box[1], box[3], from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "read from AMR Health (read-only): %d scan(s) near the box, %d placed on %s", len(use), len(ok), mapName)
	if rejected > 0 {
		fmt.Fprintf(&b, ", %d could not be placed", rejected)
	}
	if weak > 0 {
		fmt.Fprintf(&b, "; %d of the placed ones fit poorly (20 to 40 %% of points on walls) and were left where the robot said it was", weak)
	}
	if noPoints > 0 {
		fmt.Fprintf(&b, ", %d without points or position", noPoints)
	}
	if doubtful > 0 {
		fmt.Fprintf(&b, "; %d were taken while the robot's confidence was below 0.5", doubtful)
	}
	b.WriteString("\nEach scan is one moment. It is placed by starting at the position the server stored and looking around it for the best fit to the walls.\n\nEACH ROBOT AGAINST THE MAP\n")
	fmt.Fprintf(&b, "  %-9s %5s  %-22s %s\n", "robot", "scans", "laser points on a wall", "typical move from the stored position")
	for _, r := range robs {
		note := ""
		if r.Odd {
			note = "   <-- clearly below the other robots: check its scanners"
		}
		fmt.Fprintf(&b, "  %-9s %5d  %-22s %.2f m%s\n", r.Robot, r.Scans, fmt.Sprintf("%.0f %%", r.Match), r.Moved, note)
	}
	if len(robs) < 3 {
		b.WriteString("  Fewer than 3 robots: the scanners cannot be compared with each other yet.\n")
	} else if len(odd) == 0 {
		b.WriteString("  The robots agree with each other: no scanner stands out.\n")
	}
	fmt.Fprintf(&b, "\n%s IN THIS BOX, %d wall dots\n", mapName, nw)
	fmt.Fprintf(&b, "  seen by the lasers                      %5.1f %%\n", pct(len(seen), nw))
	fmt.Fprintf(&b, "  not seen in these scans                 %5.1f %%   (gone, hidden, or simply not looked at yet)\n", pct(len(unseen), nw))
	fmt.Fprintf(&b, "\nSEEN BY THE LASERS, NOT IN THE MAP: %d cells of 10 cm\n", len(always)+len(sometimes))
	if len(ok) > 1 {
		fmt.Fprintf(&b, "  in most scans that could see it         %5d   something permanent that the map does not have\n", len(always))
		fmt.Fprintf(&b, "  in few of them                          %5d   something passing through or put down for a while\n", len(sometimes))
	} else {
		b.WriteString("  With one scan a permanent object and a passing one look the same.\n")
	}
	if weak > 0 {
		b.WriteString("\nWhere a scan fits poorly, the robot's own position may be off as well, so the colours on the map show a disagreement, not exactly which wall is wrong. Look at them on the map: red = map wall the lasers do not see, blue and pink = seen but not in the map.\n")
	}
	if len(ok) < 5 {
		b.WriteString("\nOnly a few scans so far: read this as a first look, not as a finding.\n")
	}
	b.WriteString("\nA single scan cannot show a position jump or a misplaced piece of map. That needs a recorded drive.\n")
	avg := 0.0
	for _, p := range ok {
		avg += p.match
	}
	return &Result{Summary: b.String(), Data: map[string]any{
		"fits": []mapFit{{Map: mapName, Match: avg / float64(len(ok)), Walls: nw}}, "bestOther": "", "box": box, "scans": len(ok), "drives": len(ok), "robots": robs,
		"seenPct": pct(len(seen), nw), "unseenPct": pct(len(unseen), nw), "always": len(always), "sometimes": len(sometimes),
		"ptsSeen": f32(seen), "ptsUnseen": f32(unseen), "ptsAlways": f32(always), "ptsSometimes": f32(sometimes),
	}}, nil
}

func init() {
	reg(&Tool{Name: "stored_scans", Desc: "READ-ONLY: list the laser scans that the plant's AMR Health server collected by itself (version 0.7.74 or newer): per robot how many there are near a box and in a time range, why each was taken (a regular sample, position lost, low confidence, LiDAR alarm) and the confidence at that moment. Needs the AMR Health address in History sources and a sign-in stored once. Use it to see whether automatic laser evidence exists for a place before asking for a recording.",
		Params: obj(`"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"optional: only scans taken near this box"},"hours":{"type":"number","description":"how many hours back (default 48)"},"from":{"type":"string"},"to":{"type":"string"},"robot":{"type":"string","description":"optional: one robot"}`),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Box             []float64
				Hours           float64
				From, To, Robot string
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			from, to, err := window(p.Hours, p.From, p.To)
			if err != nil {
				return nil, err
			}
			var box *[4]float64
			if len(p.Box) == 4 {
				b, _ := box4(p.Box)
				box = &b
			}
			all, err := c.App.scanList(c, nil, from, to, strings.TrimSpace(p.Robot))
			if err != nil {
				return nil, err
			}
			list := all
			nearest, nearestD := -1, math.Inf(1)
			if box != nil {
				list = nil
				for i, s := range all {
					if s.X == nil || s.Y == nil {
						continue
					}
					dx := math.Max(0, math.Max(box[0]-*s.X, *s.X-box[2]))
					dy := math.Max(0, math.Max(box[1]-*s.Y, *s.Y-box[3]))
					d := math.Hypot(dx, dy)
					if d <= armMargin {
						list = append(list, s)
					}
					if d < nearestD {
						nearest, nearestD = i, d
					}
				}
			}
			type row struct {
				Robot  string `json:"robot"`
				Scans  int    `json:"scans"`
				Usable int    `json:"usable"`
				Low    int    `json:"low"`
				Newest string `json:"newest"`
			}
			by := map[string]*row{}
			var names []string
			reasons := map[string]int{}
			for _, s := range list {
				r := by[s.AMR]
				if r == nil {
					r = &row{Robot: s.AMR, Newest: s.CapturedAt.Local().Format("2006-01-02 15:04")}
					by[s.AMR] = r
					names = append(names, s.AMR)
				}
				r.Scans++
				if scanUsable(s) {
					r.Usable++
				}
				if s.Confidence != nil && *s.Confidence < 0.5 {
					r.Low++
				}
				reasons[s.Reason]++
			}
			sort.Strings(names)
			var b strings.Builder
			where := "anywhere in the plant"
			if box != nil {
				where = fmt.Sprintf("near the box x %.1f..%.1f y %.1f..%.1f", box[0], box[2], box[1], box[3])
			}
			fmt.Fprintf(&b, "STORED LASER SCANS on AMR Health, %s, %s to %s: %d\n", where, from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), len(list))
			var rows []row
			for _, n := range names {
				r := by[n]
				rows = append(rows, *r)
				fmt.Fprintf(&b, "  %-9s %4d scan(s), %d with points and a position, %d taken at confidence below 0.5, newest %s\n", r.Robot, r.Scans, r.Usable, r.Low, r.Newest)
			}
			if len(list) == 0 && box != nil && len(all) > 0 {
				s := all[nearest]
				fmt.Fprintf(&b, "  none near this box. AMR Health holds %d scan(s) in that time, all taken elsewhere; the nearest was %.0f m from the box (%s at %s, %s).\n", len(all), nearestD, s.AMR, scanPlace(s), s.CapturedAt.Local().Format("02 Jan 15:04"))
				b.WriteString("  A stored scan helps here only if a robot was within about 12 m of the box when it was taken.\n")
			} else if len(list) == 0 {
				b.WriteString("  none. Scans are stored only for the robots named in the AMR Health settings, at their sample times or when they have a problem.\n")
			} else {
				var rs []string
				for k, n := range reasons {
					rs = append(rs, fmt.Sprintf("%s %d", k, n))
				}
				sort.Strings(rs)
				b.WriteString("  why they were taken: " + strings.Join(rs, ", ") + "\n")
			}
			// where the newest ones were taken
			type at struct {
				Robot  string  `json:"robot"`
				When   string  `json:"when"`
				Reason string  `json:"reason"`
				Conf   float64 `json:"conf"`
				X      float64 `json:"x"`
				Y      float64 `json:"y"`
				Place  string  `json:"place"`
			}
			var places []at
			if len(all) > 0 {
				b.WriteString("\nWHERE THE NEWEST SCANS WERE TAKEN (anywhere in the plant)\n")
			}
			for i, s := range all {
				if i == 15 {
					fmt.Fprintf(&b, "  ... %d more\n", len(all)-i)
					break
				}
				w := at{Robot: s.AMR, When: s.CapturedAt.Local().Format("02 Jan 15:04"), Reason: s.Reason, Place: scanPlace(s), Conf: -1}
				if s.Confidence != nil {
					w.Conf = *s.Confidence
				}
				if s.X != nil && s.Y != nil {
					w.X, w.Y = *s.X, *s.Y
				}
				places = append(places, w)
				fmt.Fprintf(&b, "  %s  %-8s %-15s confidence %.2f  at %s\n", w.When, w.Robot, w.Reason, w.Conf, w.Place)
			}
			total := len(all)
			nearestPlace := ""
			if nearest >= 0 {
				nearestPlace = fmt.Sprintf("%s at %s, %s", all[nearest].AMR, scanPlace(all[nearest]), all[nearest].CapturedAt.Local().Format("02 Jan 15:04"))
			}
			return &Result{Summary: b.String(), Data: map[string]any{"robots": rows, "total": len(list), "stored": total, "where": places, "nearest": nearestD, "nearestPlace": nearestPlace}}, nil
		}})
	reg(&Tool{Name: "scan_evidence", Heavy: true, Desc: "READ-ONLY: take the laser scans that AMR Health stored near a box, place each one on a map, and report: each robot's share of laser points on walls (a robot clearly below the others has a scanner problem), which walls of the map in the box were seen, and what the lasers see that the map does not have (in most scans = permanent, in few = passing through). Works without anyone recording. A single scan cannot show a position jump or a misplaced piece: that needs a recorded drive (box_evidence, replay).",
		Params: obj(pMap+`,"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4},"hours":{"type":"number","description":"how many hours back (default 48)"},"from":{"type":"string"},"to":{"type":"string"},"robots":{"type":"array","items":{"type":"string"},"description":"optional: only these robots"}`, "map", "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Map      string
				Box      []float64
				Hours    float64
				From, To string
				Robots   []string
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			b, err := box4(p.Box)
			if err != nil {
				return nil, err
			}
			from, to, err := window(p.Hours, p.From, p.To)
			if err != nil {
				return nil, err
			}
			return c.App.scanEvidence(c, b, p.Map, from, to, p.Robots)
		}})
}
