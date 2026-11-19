package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"drishtimap/help"
	"drishtimap/lasertrack"
	"drishtimap/mapedit"
	"drishtimap/seer"
	"drishtimap/smap"
)

// ---------------------------------------------------------------- hand edits

// EditSession is the map being edited by hand in the window: a copy of the base map with the edits
// applied so far. It lives in memory only; Save writes it as a candidate.
type EditSession struct {
	Base string
	Ed   *mapedit.Editor
	Ops  []mapedit.EditOp
}

var editMu sync.Mutex
var editSess *EditSession

func (a *App) donor(name string) (*smap.Map, error) { return a.Map(name) }

func (a *App) newEdit(base string, ops []mapedit.EditOp) (*EditSession, error) {
	bm, err := a.Map(base)
	if err != nil {
		return nil, err
	}
	s := &EditSession{Base: base, Ed: mapedit.NewEditor(bm.Clone(), a.donor)}
	for i, op := range ops {
		if _, err := s.Ed.Apply(op); err != nil {
			return nil, fmt.Errorf("edit %d (%s): %v", i+1, op.Op, err)
		}
		s.Ops = append(s.Ops, op)
	}
	return s, nil
}

// EditView is what the window needs to show the edit in progress.
type EditView struct {
	Base string    `json:"base"`
	Log  []string  `json:"log"`
	Map  *smap.Map `json:"-"`
}

func editView(s *EditSession) *EditView {
	if s == nil {
		return nil
	}
	return &EditView{Base: s.Base, Log: append([]string{}, s.Ed.Log...), Map: s.Ed.M}
}

// EditStart begins (or restarts) hand editing of a map.
func (a *App) EditStart(base string) (*EditView, error) {
	s, err := a.newEdit(base, nil)
	if err != nil {
		return nil, err
	}
	editMu.Lock()
	editSess = s
	editMu.Unlock()
	return editView(s), nil
}

// EditState returns the edit in progress (nil when there is none).
func (a *App) EditState() *EditView {
	editMu.Lock()
	defer editMu.Unlock()
	return editView(editSess)
}

// EditApply applies one more edit.
func (a *App) EditApply(op mapedit.EditOp) (*EditView, string, error) {
	editMu.Lock()
	defer editMu.Unlock()
	if editSess == nil {
		return nil, "", fmt.Errorf("no map is being edited; press Start editing first")
	}
	note, err := editSess.Ed.Apply(op)
	if err != nil {
		return nil, "", err
	}
	editSess.Ops = append(editSess.Ops, op)
	return editView(editSess), note, nil
}

// EditUndo removes the last edit (the map is rebuilt from the base).
func (a *App) EditUndo() (*EditView, error) {
	editMu.Lock()
	defer editMu.Unlock()
	if editSess == nil || len(editSess.Ops) == 0 {
		return editView(editSess), fmt.Errorf("nothing to undo")
	}
	s, err := a.newEdit(editSess.Base, editSess.Ops[:len(editSess.Ops)-1])
	if err != nil {
		return nil, err
	}
	editSess = s
	return editView(s), nil
}

// EditCancel drops the edit in progress.
func (a *App) EditCancel() {
	editMu.Lock()
	editSess = nil
	editMu.Unlock()
}

// EditSave writes the edit in progress as a candidate and ends the session.
func (a *App) EditSave(name string) (*Result, error) {
	editMu.Lock()
	defer editMu.Unlock()
	if editSess == nil {
		return nil, fmt.Errorf("no map is being edited")
	}
	if len(editSess.Ops) == 0 {
		return nil, fmt.Errorf("no edit has been made; nothing to save")
	}
	r, err := a.saveEdit(editSess, name)
	if err != nil {
		return nil, err
	}
	editSess = nil
	return r, nil
}

func (a *App) saveEdit(s *EditSession, name string) (*Result, error) {
	n := strings.TrimSuffix(strings.TrimSpace(name), ".smap")
	mapedit.FixHeader(s.Ed.M, n)
	var b strings.Builder
	for i, l := range s.Ed.Log {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, l)
	}
	sum, err := a.saveCandidate(s.Ed.M, n, s.Base, fmt.Sprintf("%d hand edit(s)", len(s.Ops)), "edits, in order:\n"+b.String(), s.Ed.Intended)
	if err != nil {
		return nil, err
	}
	return &Result{Summary: sum, Data: map[string]any{"candidate": n, "base": s.Base}}, nil
}

// ---------------------------------------------------------------- fleet

// FleetRow is one robot's answer in the fleet map check.
type FleetRow struct {
	Robot      string   `json:"robot"`
	IP         string   `json:"ip"`
	OK         bool     `json:"ok"`
	Error      string   `json:"error,omitempty"`
	Map        string   `json:"map"`
	MD5        string   `json:"md5"`
	Known      string   `json:"known,omitempty"` // workspace map with the same content
	Confidence float64  `json:"confidence"`
	Station    string   `json:"station"`
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	Maps       []string `json:"maps"`
	Odd        bool     `json:"odd"` // not the map most robots use
}

func init() {
	var ops []string
	for _, o := range mapedit.OpNames {
		ops = append(ops, o[0]+": "+o[1])
	}
	opEnum := `"enum":["` + strings.Join(func() (n []string) {
		for _, o := range mapedit.OpNames {
			n = append(n, o[0])
		}
		return
	}(), `","`) + `"]`

	reg(&Tool{Name: "detect_problems", Heavy: true, Desc: "Scan a map for trouble and list every finding with its place. Without a recording: feature lines on or beside paths (virtual-laser fault), paths running through mapped wall dots, stations and paths that do not connect, header faults. With a recording (drive): also where the map makes a robot jump, where the wall match is low, and whether the drive fits the map as one rigid track. Start here when asked what is wrong with a map.",
		Params: obj(pMap+","+pDrive+","+pFromTo, "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Map, Drive string
				From, To   int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			m, err := c.App.Map(p.Map)
			if err != nil {
				return nil, err
			}
			fs := mapedit.Detect(m)
			var b strings.Builder
			fmt.Fprintf(&b, "DETECT on %s (md5 %s)\n", p.Map, m.MD5)
			data := map[string]any{"map": p.Map}
			if p.Drive != "" {
				ss, ch, _, r, err := c.App.follow(c, p.Drive, p.Map, p.From, p.To)
				if err != nil {
					return nil, err
				}
				var dyn []mapedit.Finding
				for _, j := range r.Jumps {
					dyn = append(dyn, mapedit.Finding{Kind: "jump", Level: "fault", X: j.X, Y: j.Y, HasXY: true, Near: j.Near,
						Text: fmt.Sprintf("JUMP: the map placement steps %.2f m (turn %+.1f deg) at scans %d-%d of %s", j.Step, j.TurnDeg, j.I0, j.I1, p.Drive),
						Fix:  "a piece of the map is in the wrong place: Find rigid pieces, Measure turn, Build (WI-02, WI-03)"})
				}
				// rigid fit on the longest part of the laser track that has no restart in it
				// (a restart is the robot being relocalised or samples missing, not a map fault)
				seg, s0 := lasertrack.Range{}, 0
				for i := range ch {
					if i > 0 && ch[i].Break {
						s0 = i
					}
					if ch[i].I-ch[s0].I > seg.To-seg.From {
						seg = lasertrack.Range{From: ch[s0].I, To: ch[i].I}
					}
				}
				if al, err := lasertrack.AlignTrack(ch, r.Rows, []lasertrack.Range{seg}, 45); err == nil {
					data["rms"], data["rigidRange"] = al.RMS, []int{seg.From, seg.To}
					if al.RMS > 0.15 && len(r.Jumps) > 0 {
						dyn = append(dyn, mapedit.Finding{Kind: "not_rigid", Level: "fault", Text: fmt.Sprintf("the drive %s does not fit this map as one rigid track (scans %d-%d: fit %.2f m rms, worst %.2f m; a good map gives about 0.1 m)", p.Drive, seg.From, seg.To, al.RMS, al.Max),
							Fix: "together with the jump above: a piece of the map is misplaced. Find rigid pieces (WI-02)"})
					} else if al.RMS > 0.15 {
						// no step anywhere: on a long drive with stops and turns the laser track itself drifts
						dyn = append(dyn, mapedit.Finding{Kind: "not_rigid", Level: "warn", Text: fmt.Sprintf("the drive %s (%.0f m) does not fit this map as one rigid track (scans %d-%d: fit %.2f m rms) although no step reaches 0.5 m. On a long drive with stops and turns on the spot this is usually drift of the laser track, not the map", p.Drive, lasertrack.TrackLength(ch), seg.From, seg.To, al.RMS),
							Fix: "Find rigid pieces: long pieces at about 0.1 m rms with small steps between them = the map is fine. To be sure, record a shorter drive without stops through the area (WI-01)"})
					} else {
						fmt.Fprintf(&b, "the drive %s fits the map as one rigid track (scans %d-%d: %.2f m rms)\n", p.Drive, seg.From, seg.To, al.RMS)
					}
				}
				// runs of scans with poor wall match
				type run struct {
					i0, i1, n int
					sum       float64
					x, y      float64
					near      string
				}
				var cur *run
				flush := func() {
					if cur != nil && cur.n >= 8 {
						dyn = append(dyn, mapedit.Finding{Kind: "low_match", Level: "warn", X: cur.x, Y: cur.y, HasXY: true, Near: cur.near,
							Text: fmt.Sprintf("low wall match: scans %d-%d average %.0f %% of laser points on map walls (a robot reports low confidence here)", cur.i0, cur.i1, cur.sum/float64(cur.n)),
							Fix:  "look at the lasers on the map here (Replay): moved racks or bins are normal; walls that are missing or doubled need a rescan or Take walls (WI-06)"})
					}
					cur = nil
				}
				for _, row := range r.Rows {
					if row.Fit < 40 {
						if cur == nil {
							cur = &run{i0: row.I, x: row.X, y: row.Y, near: row.Near}
						}
						cur.i1, cur.n, cur.sum = row.I, cur.n+1, cur.sum+row.Fit
						if cur.n == 4 {
							cur.x, cur.y, cur.near = row.X, row.Y, row.Near
						}
					} else {
						flush()
					}
				}
				flush()
				fs = append(dyn, fs...)
				data["rows"], data["stretches"], data["cloud"] = r.Rows, r.Stretches, cloud(ss, r.Rows)
			} else {
				b.WriteString("map file only. Give a recording as well to find jumps and low wall match: a misplaced piece of map cannot be seen in the file alone.\n")
			}
			nf, nw := 0, 0
			for _, f := range fs {
				switch f.Level {
				case "fault":
					nf++
				case "warn":
					nw++
				}
			}
			fmt.Fprintf(&b, "%d fault(s), %d warning(s), %d note(s)\n\n", nf, nw, len(fs)-nf-nw)
			b.WriteString(mapedit.DetectText(fs, 15))
			if fs == nil {
				fs = []mapedit.Finding{}
			}
			data["findings"] = fs
			return &Result{Summary: b.String(), Data: data}, nil
		}})

	reg(&Tool{Name: "build_edit", Desc: "Write a candidate map from a list of hand edits applied in order to a copy of the base map. Edits: " + strings.Join(ops, "; ") + ". Coordinates are metres in the base map. The candidate is checked automatically; objects edited by hand are listed in the report instead of failing the 'identical to base' checks, but clearance, feature-line and structure checks still apply. It is never loaded on a robot.",
		Params: obj(`"base":{"type":"string","description":"map to start from (download the robot's current map first to keep edits made on the robot)"},"name":{"type":"string","description":"file name and map name of the candidate"},"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{"op":{"type":"string",`+opEnum+`},"lines":{"type":"array","items":{"type":"integer"}},"at":{"type":"array","items":{"type":"number"}},"box":{"type":"array","items":{"type":"number"},"description":"[x0,y0,x1,y1]"},"region":{"type":"array","items":{"type":"array","items":{"type":"number"}}},"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"station":{"type":"string"},"key":{"type":"string"},"value":{"description":"number, true/false or text, as the property needs"},"x":{"type":"number"},"y":{"type":"number"},"dx":{"type":"number"},"dy":{"type":"number"},"dirDeg":{"type":"number"},"ignoreDir":{"type":"boolean"},"rotDeg":{"type":"number"},"pivot":{"type":"array","items":{"type":"number"}},"tx":{"type":"number"},"ty":{"type":"number"},"donor":{"type":"string"},"mode":{"type":"string","enum":["replace","add"]},"withLines":{"type":"boolean"},"rssi":{"type":"boolean"},"withPaths":{"type":"boolean"},"name":{"type":"string"},"like":{"type":"string"},"from":{"type":"string"},"to":{"type":"string"}},"required":["op"]}}`, "base", "name", "edits"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Base, Name string
				Edits      []mapedit.EditOp
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if len(p.Edits) == 0 {
				return nil, fmt.Errorf("no edits given; no file written")
			}
			s, err := c.App.newEdit(p.Base, p.Edits)
			if err != nil {
				return nil, fmt.Errorf("%v; no file written", err)
			}
			return c.App.saveEdit(s, p.Name)
		}})

	reg(&Tool{Name: "fleet_maps", Desc: "READ-ONLY: ask every robot in the workspace list which map it is using now (name and checksum), its confidence and station, and report which robots do NOT run the same map as the others. Robots that are switched off are listed as not reachable. With download=true each different map is copied once into the workspace so it can be compared.",
		Params: obj(`"download":{"type":"boolean","description":"copy each different current map into the workspace maps folder"}`),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Download bool }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			return c.App.fleetMaps(c, p.Download)
		}})

	reg(&Tool{Name: "help", Desc: "Read the built-in work instructions (WI-..) and knowledge base (KB-..). Without a topic: the list of articles. With a topic id: the article. Follow the work instructions when guiding the user step by step.",
		Params: obj(`"topic":{"type":"string","description":"article id, e.g. WI-02 or KB-03"}`),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Topic string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if p.Topic != "" {
				if t := help.Get(p.Topic); t != "" {
					return &Result{Summary: t}, nil
				}
			}
			var b strings.Builder
			if p.Topic != "" {
				fmt.Fprintf(&b, "there is no article %q. ", p.Topic)
			}
			b.WriteString("Articles:\n")
			for _, a := range help.List() {
				fmt.Fprintf(&b, "  %-6s %s\n", a.ID, a.Title)
			}
			return &Result{Summary: b.String(), Data: help.List()}, nil
		}})
}

func (a *App) fleetMaps(c *Ctx, download bool) (*Result, error) {
	robots := a.Robots()
	if len(robots) == 0 {
		return nil, fmt.Errorf("the robot list is empty; add robots first (name and IP address)")
	}
	rows := make([]FleetRow, len(robots))
	var wg sync.WaitGroup
	var done int
	var mu sync.Mutex
	for i, r := range robots {
		wg.Add(1)
		go func(i int, r Robot) {
			defer wg.Done()
			row := FleetRow{Robot: r.Name, IP: r.IP}
			ctx, cancel := context.WithTimeout(c, 12*time.Second)
			st, err := seer.ReadStatus(ctx, r.IP)
			cancel()
			if err != nil {
				row.Error = "not reachable"
			} else {
				row.OK, row.Map, row.MD5, row.Confidence, row.Station, row.X, row.Y, row.Maps = true, st.CurrentMap, st.CurrentMapMD5, st.Confidence, st.CurrentStation, st.X, st.Y, st.Maps
				if row.Station == "" {
					row.Station = st.LastStation
				}
			}
			mu.Lock()
			rows[i] = row
			done++
			c.progress(done, len(robots), "asking the robots")
			mu.Unlock()
		}(i, r)
	}
	wg.Wait()
	if c.Err() != nil {
		return nil, c.Err()
	}
	// which workspace map has the same content
	known := map[string]string{}
	for _, folder := range []string{"maps", "candidates"} {
		for _, f := range a.listMaps(folder) {
			if m, err := a.Map(f.Name); err == nil {
				if _, ok := known[m.MD5]; !ok {
					known[m.MD5] = f.Name
				}
			}
		}
	}
	count := map[string]int{}
	up := 0
	for _, r := range rows {
		if r.OK {
			up++
			count[r.MD5]++
		}
	}
	major, best := "", 0
	for k, n := range count {
		if n > best || (n == best && k < major) {
			major, best = k, n
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FLEET MAP CHECK (read-only), %d of %d robots answered, %s\n", up, len(rows), time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "%-8s %-15s %-22s %-10s %-5s %-8s %s\n", "robot", "address", "current map", "checksum", "conf", "station", "")
	for i := range rows {
		r := &rows[i]
		if !r.OK {
			fmt.Fprintf(&b, "%-8s %-15s %s\n", r.Robot, r.IP, "not reachable (switched off, or not on this network)")
			continue
		}
		r.Known = known[r.MD5]
		r.Odd = len(count) > 1 && r.MD5 != major
		note := ""
		if r.Odd {
			note = "<-- DIFFERENT MAP"
		}
		if r.Known != "" {
			note += "  = " + r.Known + " in this workspace"
		}
		fmt.Fprintf(&b, "%-8s %-15s %-22s %-10s %-5.2f %-8s %s\n", r.Robot, r.IP, r.Map, short(r.MD5), r.Confidence, r.Station, note)
	}
	switch {
	case up == 0:
		b.WriteString("\nNo robot answered. Is this PC on the robot network?\n")
	case len(count) == 1:
		fmt.Fprintf(&b, "\nAll %d robots that answered use the same map content (checksum %s).\n", up, short(major))
	default:
		fmt.Fprintf(&b, "\n%d DIFFERENT maps are in use:\n", len(count))
		var ks []string
		for k := range count {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			return count[ks[i]] > count[ks[j]] || (count[ks[i]] == count[ks[j]] && ks[i] < ks[j])
		})
		for _, k := range ks {
			var who []string
			for _, r := range rows {
				if r.OK && r.MD5 == k {
					who = append(who, r.Robot)
				}
			}
			fmt.Fprintf(&b, "  %s  %d robot(s): %s\n", short(k), count[k], strings.Join(who, ", "))
		}
		b.WriteString("Robots on different maps do not agree where the walls are. Download both and use Compare to see the difference.\n")
	}
	var got []string
	if download {
		seen := map[string]bool{}
		for _, r := range rows {
			if !r.OK || seen[r.MD5] || known[r.MD5] != "" {
				continue
			}
			seen[r.MD5] = true
			c.prog(0.5, "downloading "+r.Map+" from "+r.Robot)
			bts, err := seer.DownloadMap(c, r.IP, r.Map)
			if err != nil {
				fmt.Fprintf(&b, "download from %s failed: %v\n", r.Robot, err)
				continue
			}
			if _, err := smap.FromBytes(bts); err != nil {
				fmt.Fprintf(&b, "download from %s is not a map file: %v\n", r.Robot, err)
				continue
			}
			n, err := safeName(fmt.Sprintf("%s_%s_%s", r.Map, r.Robot, time.Now().Format("20060102-1504")))
			if err != nil {
				continue
			}
			if err := os.WriteFile(a.ws("maps", n+".smap"), bts, 0644); err == nil {
				got = append(got, n)
				fmt.Fprintf(&b, "downloaded %s (checksum %s) from %s -> maps\\%s.smap\n", r.Map, short(r.MD5), r.Robot, n)
			}
		}
	}
	return &Result{Summary: b.String(), Data: map[string]any{"rows": rows, "different": len(count), "downloaded": got}}, nil
}

func short(md5 string) string {
	if len(md5) > 8 {
		return md5[:8]
	}
	return md5
}
