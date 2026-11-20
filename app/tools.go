package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"drishtimap/lasertrack"
	"drishtimap/mapedit"
	"drishtimap/seer"
	"drishtimap/smap"
)

// Result is what a tool returns. Summary is plain text (shown in the window and, for the AI
// assistant, the ONLY part that is sent to the model). Data is for drawing in the window.
type Result struct {
	Summary string `json:"summary"`
	Data    any    `json:"data,omitempty"`
}

// Ctx is passed to a running tool.
type Ctx struct {
	context.Context
	App  *App
	prog func(float64, string)
}

func (c *Ctx) progress(done, total int, note string) {
	if total > 0 {
		c.prog(float64(done)/float64(total), note)
	}
}

// Tool is one operation, available as a button and to the AI assistant.
type Tool struct {
	Name   string                                              `json:"name"`
	Desc   string                                              `json:"description"`
	Params json.RawMessage                                     `json:"parameters"`
	Heavy  bool                                                `json:"-"`
	Run    func(c *Ctx, args json.RawMessage) (*Result, error) `json:"-"`
}

var toolByName = map[string]*Tool{}

// Tools lists all tools in a fixed order.
var Tools []*Tool

func reg(t *Tool) { Tools = append(Tools, t); toolByName[t.Name] = t }

func obj(props string, required ...string) json.RawMessage {
	r, _ := json.Marshal(required)
	if len(required) == 0 {
		r = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(r) + `}`)
}

const (
	pMap    = `"map":{"type":"string","description":"map name as listed by list_workspace (no .smap)"}`
	pDrive  = `"drive":{"type":"string","description":"recording name as listed by list_workspace"}`
	pFromTo = `"from":{"type":"integer","description":"first sample (default 1)"},"to":{"type":"integer","description":"last sample (default: end)"}`
	pRanges = `{"type":"array","items":{"type":"array","items":{"type":"integer"},"minItems":2,"maxItems":2},"description":"sample ranges [[from,to],...]"}`
	pRegion = `"region":{"type":"array","items":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2},"description":"polygon corners [[x,y],...] in the base map's coordinates (metres)"}`
)

const notLoaded = "This file is NOT loaded on any robot. DRISHTI Map never loads maps. A person loads it in RoboShop, on ONE test robot first."

func parse(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

func ranges(r [][]int) []lasertrack.Range {
	var o []lasertrack.Range
	for _, x := range r {
		if len(x) == 2 {
			o = append(o, lasertrack.Range{From: x[0], To: x[1]})
		}
	}
	return o
}

func poly(r [][]float64) []smap.Pt {
	var o []smap.Pt
	for _, x := range r {
		if len(x) == 2 {
			o = append(o, smap.Pt{X: x[0], Y: x[1]})
		}
	}
	return o
}

func f32(pts []smap.Pt) string {
	b := make([]byte, 8*len(pts))
	for i, p := range pts {
		binary.LittleEndian.PutUint32(b[8*i:], math.Float32bits(float32(p.X)))
		binary.LittleEndian.PutUint32(b[8*i+4:], math.Float32bits(float32(p.Y)))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// chain returns the laser track of a drive, computing and saving it on first use.
func (a *App) chain(c *Ctx, drive string, from, to int) ([]*lasertrack.Sample, []lasertrack.ChainPose, error) {
	c.prog(0, "reading the recording")
	ss, dir, err := a.Samples(drive, from, to)
	if err != nil {
		return nil, nil, err
	}
	if ch, ok := lasertrack.LoadChain(dir, from, to); ok {
		return ss, ch, nil
	}
	ch := lasertrack.Chain(ss, lasertrack.ChainOpts{SkipStill: true, Progress: func(d, t int) { c.progress(d, t*2, "building the laser track") }})
	if c.Err() != nil {
		return nil, nil, c.Err()
	}
	lasertrack.SaveChain(dir, from, to, ch)
	return ss, ch, nil
}

type replayKey struct {
	drive, md5 string
	from, to   int
}

var replayCache = map[replayKey]*lasertrack.Replay{}

// follow returns the follow-replay of a drive through a map (kept in memory for the other tools).
func (a *App) follow(c *Ctx, drive, mapName string, from, to int) ([]*lasertrack.Sample, []lasertrack.ChainPose, *smap.Map, *lasertrack.Replay, error) {
	ss, ch, err := a.chain(c, drive, from, to)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	m, err := a.Map(mapName)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	k := replayKey{fmt.Sprintf("%s|%d", drive, len(ch)), m.MD5, from, to}
	a.mu.Lock()
	r := replayCache[k]
	a.mu.Unlock()
	if r == nil {
		r, err = lasertrack.Run(ss, ch, m, lasertrack.ReplayOpts{Progress: func(d, t int) { c.progress(t+d, t*2, "replaying the lasers through "+mapName) }})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		a.mu.Lock()
		if len(replayCache) > 12 {
			replayCache = map[replayKey]*lasertrack.Replay{}
		}
		replayCache[k] = r
		a.mu.Unlock()
	}
	return ss, ch, m, r, nil
}

func cloud(ss []*lasertrack.Sample, rows []lasertrack.Row) string {
	byI := map[int]*lasertrack.Sample{}
	n := 0
	for _, s := range ss {
		byI[s.I] = s
		n += len(s.RP)
	}
	skip := n/300000 + 1
	var pts []smap.Pt
	k := 0
	for _, r := range rows {
		s := byI[r.I]
		if s == nil {
			continue
		}
		c, sn := math.Cos(r.Deg*math.Pi/180), math.Sin(r.Deg*math.Pi/180)
		for _, p := range s.RP {
			if k++; k%skip == 0 {
				pts = append(pts, smap.Pt{X: r.X + c*p.X - sn*p.Y, Y: r.Y + sn*p.X + c*p.Y})
			}
		}
	}
	return f32(pts)
}

func faultsOnly(fs []mapedit.Finding) (out []mapedit.Finding) {
	for _, f := range fs {
		if f.Level != "info" {
			out = append(out, f)
		}
	}
	return
}

func (a *App) saveCandidate(m *smap.Map, name, base, what string, extra string, byHand map[string]bool) (string, error) {
	n, err := safeName(strings.TrimSuffix(name, ".smap"))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(a.ws("maps", n+".smap")); err == nil {
		return "", fmt.Errorf("a source map named %q already exists; choose another name", n)
	}
	if err := m.Save(a.ws("candidates", n+".smap")); err != nil {
		return "", err
	}
	bm, err := a.Map(base)
	if err != nil {
		return "", err
	}
	cm, err := a.Map(n)
	if err != nil {
		return "", err
	}
	v := mapedit.VerifyWith(bm, cm, byHand)
	var b strings.Builder
	fmt.Fprintf(&b, "candidate %s.smap written to the candidates folder\n", n)
	fmt.Fprintf(&b, "sha256 %s\nmd5 %s   map name inside the file: %s\n", cm.SHA256, cm.MD5, cm.Name())
	fmt.Fprintf(&b, "built from %s (md5 %s): %s\n", base, bm.MD5, what)
	b.WriteString(extra)
	b.WriteString("\nCHECKS against " + base + ":\n" + v.Text())
	if fs := faultsOnly(mapedit.Detect(cm)); len(fs) > 0 {
		b.WriteString("\nDETECTOR on the candidate itself:\n" + mapedit.DetectText(fs, 10))
	}
	b.WriteString("\n" + notLoaded + "\n")
	os.WriteFile(a.ws("candidates", n+".report.txt"), []byte(strings.ReplaceAll(b.String(), "\n", "\r\n")), 0644)
	return b.String(), nil
}

func init() {
	reg(&Tool{Name: "list_workspace", Desc: "List the maps, candidate maps, laser recordings and robots in the current workspace.", Params: obj(``),
		Run: func(c *Ctx, _ json.RawMessage) (*Result, error) {
			a := c.App
			maps, cands, drives, robots := a.listMaps("maps"), a.listMaps("candidates"), a.listDrives(), a.Robots()
			var b strings.Builder
			fmt.Fprintf(&b, "workspace: %s\nmaps:\n", a.Cfg.Workspace)
			for _, m := range maps {
				fmt.Fprintf(&b, "  %s  (%.1f MB, %s)\n", m.Name, m.SizeMB, m.Modified)
			}
			b.WriteString("candidates (built by this tool, not on any robot):\n")
			for _, m := range cands {
				fmt.Fprintf(&b, "  %s  (%.1f MB, %s)\n", m.Name, m.SizeMB, m.Modified)
			}
			b.WriteString("recordings:\n")
			for _, d := range drives {
				fmt.Fprintf(&b, "  %s  (%d samples)\n", d.Name, d.Samples)
			}
			b.WriteString("robots:\n")
			for _, r := range robots {
				fmt.Fprintf(&b, "  %s  %s\n", r.Name, r.IP)
			}
			return &Result{Summary: b.String(), Data: map[string]any{"workspace": a.Cfg.Workspace, "maps": maps, "candidates": cands, "drives": drives, "robots": robots}}, nil
		}})

	reg(&Tool{Name: "map_info", Desc: "Counts, bounds, checksum and map name of one map file.", Params: obj(pMap, "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Map string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			m, err := c.App.Map(p.Map)
			if err != nil {
				return nil, err
			}
			i := mapedit.Describe(m)
			s := fmt.Sprintf("%s: map name %q, %.1f MB\nmd5 %s\nsha256 %s\nwall dots %d, reflection points %d, reflectors %d, feature lines %d\nstations %d %v, paths %d, bins %d, areas %d %v\nbounds x %.1f..%.1f, y %.1f..%.1f, resolution %v\n",
				p.Map, i.MapName, i.SizeMB, i.MD5, i.SHA256, i.Walls, i.Rssi, i.Reflectors, i.Lines, i.Stations, i.StationTypes, i.Paths, i.Bins, i.Areas, i.AreaTypes, i.MinX, i.MaxX, i.MinY, i.MaxY, i.Resolution)
			return &Result{Summary: s, Data: i}, nil
		}})

	reg(&Tool{Name: "stations", Desc: "Positions of stations on a map: by name, inside a box, or inside a polygon. Use it to choose a region or to read where a station is.",
		Params: obj(pMap+`,"names":{"type":"array","items":{"type":"string"}},"box":{"type":"array","items":{"type":"number"},"description":"[x0,y0,x1,y1]"},`+pRegion, "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Map    string
				Names  []string
				Box    []float64
				Region [][]float64
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			m, err := c.App.Map(p.Map)
			if err != nil {
				return nil, err
			}
			want := map[string]bool{}
			for _, n := range p.Names {
				want[strings.ToUpper(n)] = true
			}
			pg := poly(p.Region)
			var b strings.Builder
			n := 0
			for _, s := range m.Stations() {
				if len(want) > 0 && !want[strings.ToUpper(s.Name)] {
					continue
				}
				if len(p.Box) == 4 && (s.Pos.X < math.Min(p.Box[0], p.Box[2]) || s.Pos.X > math.Max(p.Box[0], p.Box[2]) || s.Pos.Y < math.Min(p.Box[1], p.Box[3]) || s.Pos.Y > math.Max(p.Box[1], p.Box[3])) {
					continue
				}
				if len(pg) >= 3 && !smap.InPoly(s.Pos, pg) {
					continue
				}
				if n++; n <= 250 {
					fmt.Fprintf(&b, "%-7s (%7.2f, %7.2f) dir %6.1f deg  %s\n", s.Name, s.Pos.X, s.Pos.Y, s.Dir*180/math.Pi, s.Class)
				}
			}
			fmt.Fprintf(&b, "%d station(s)", n)
			if n > 250 {
				b.WriteString(" (first 250 shown)")
			}
			return &Result{Summary: b.String()}, nil
		}})

	reg(&Tool{Name: "compare_maps", Desc: "List every difference between two maps: stations, paths (including direction changes), wall dots, feature lines, areas, header.",
		Params: obj(`"a":{"type":"string","description":"first map"},"b":{"type":"string","description":"second map"}`, "a", "b"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ A, B string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			ma, err := c.App.Map(p.A)
			if err != nil {
				return nil, err
			}
			mb, err := c.App.Map(p.B)
			if err != nil {
				return nil, err
			}
			d := mapedit.Compare(ma, mb)
			ln := func(l []smap.Line) (o [][4]float64) {
				for _, x := range l {
					o = append(o, [4]float64{x.A.X, x.A.Y, x.B.X, x.B.Y})
				}
				return
			}
			return &Result{Summary: d.Text(p.A, p.B), Data: map[string]any{"linesOnlyA": ln(d.LinesA), "linesOnlyB": ln(d.LinesB)}}, nil
		}})

	reg(&Tool{Name: "check_candidate", Desc: "Safety checks of a candidate map against the map it was built from: nothing lost, unmoved stations and paths identical, path properties kept, no path or station closer to a wall, no feature line on a path (virtual-laser fault), header valid.",
		Params: obj(`"base":{"type":"string","description":"the map the candidate was built from"},"candidate":{"type":"string"}`, "base", "candidate"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Base, Candidate string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			mb, err := c.App.Map(p.Base)
			if err != nil {
				return nil, err
			}
			mc, err := c.App.Map(p.Candidate)
			if err != nil {
				return nil, err
			}
			v := mapedit.Verify(mb, mc)
			return &Result{Summary: "checks of " + p.Candidate + " against " + p.Base + ":\n" + v.Text(), Data: v}, nil
		}})

	reg(&Tool{Name: "drive_summary", Desc: "What the robot itself reported during a recording: stations passed with confidence, jumps, abnormal localisation states, low-confidence stretches. Fast; uses no lasers.",
		Params: obj(pDrive+","+pFromTo, "drive"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive    string
				From, To int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			dir, err := c.App.drivePath(p.Drive)
			if err != nil {
				return nil, err
			}
			s, err := lasertrack.Summarize(dir, p.From, p.To)
			if err != nil {
				return nil, err
			}
			return &Result{Summary: "recording " + p.Drive + ":\n" + s.Text(), Data: s}, nil
		}})

	reg(&Tool{Name: "laser_track", Heavy: true, Desc: "Rebuild the true shape of a recorded drive from the lasers alone (no map). Needed by the replay tools; computed once and saved with the recording. Reports length and any breaks (places where the robot was relocalised or samples are missing).",
		Params: obj(pDrive+","+pFromTo, "drive"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive    string
				From, To int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			_, ch, err := c.App.chain(c, p.Drive, p.From, p.To)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "laser track of %s: %d scans (%d-%d), %.1f m driven\n", p.Drive, len(ch), ch[0].I, ch[len(ch)-1].I, lasertrack.TrackLength(ch))
			nb := 0
			for _, x := range ch {
				if x.Break {
					nb++
					fmt.Fprintf(&b, "  track break at scan %d near (%.1f, %.1f): the scan does not match the ones before it (robot relocalised or samples missing)\n", x.I, x.BelX, x.BelY)
				}
			}
			if nb == 0 {
				b.WriteString("no breaks: the track is one connected piece\n")
			}
			e := ch[len(ch)-1]
			fmt.Fprintf(&b, "end of drive: robot believed (%.2f, %.2f), laser track says (%.2f, %.2f): difference %.2f m (this is map error plus track drift)\n", e.BelX, e.BelY, e.X, e.Y, math.Hypot(e.BelX-e.X, e.BelY-e.Y))
			return &Result{Summary: b.String()}, nil
		}})

	reg(&Tool{Name: "replay", Heavy: true, Desc: "Drive a recording's lasers through any map file and report where the map would make a robot jump (the map placement steps 0.5 m or more between neighbouring 8 m stretches) and the wall match along the route. This is the main test of a map or a candidate. Works on maps the robot never had loaded.",
		Params: obj(pDrive+","+pMap+","+pFromTo, "drive", "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, Map string
				From, To   int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			ss, _, _, r, err := c.App.follow(c, p.Drive, p.Map, p.From, p.To)
			if err != nil {
				return nil, err
			}
			return &Result{Summary: r.Text(p.Drive + " through " + p.Map), Data: map[string]any{"rows": r.Rows, "stretches": r.Stretches, "cloud": cloud(ss, r.Rows), "map": p.Map}}, nil
		}})

	reg(&Tool{Name: "rigid_fit", Heavy: true, Desc: "Test whether a drive fits a map as ONE rigid track: the laser track is laid on the map with a single rotation+shift (fitted on the given sample ranges, default all) and the residual is reported. A candidate that is right fits with about 0.1 m rms; a map with a piece in the wrong place does not.",
		Params: obj(pDrive+","+pMap+`,"ranges":`+pRanges+","+pFromTo, "drive", "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, Map string
				Ranges     [][]int
				From, To   int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			ss, ch, m, r, err := c.App.follow(c, p.Drive, p.Map, p.From, p.To)
			if err != nil {
				return nil, err
			}
			al, err := lasertrack.AlignTrack(ch, r.Rows, ranges(p.Ranges), 45)
			if err != nil {
				return nil, err
			}
			fx, err := lasertrack.Run(ss, ch, m, lasertrack.ReplayOpts{Fixed: &al.T})
			if err != nil {
				return nil, err
			}
			s := fmt.Sprintf("%s on %s as one rigid track: fit rms %.3f m, worst %.2f m over %d scans\n", p.Drive, p.Map, al.RMS, al.Max, al.N)
			if al.RMS <= 0.15 {
				s += "=> the map agrees with the lasers as one rigid piece on these scans\n"
			} else {
				s += "=> NOT one rigid piece: part of the map is in the wrong place (see replay and find_pieces)\n"
				if l := lasertrack.TrackLength(ch); l > 120 {
					s += fmt.Sprintf("   but this drive is %.0f m long: over a long drive with stops and turns on the spot the laser track itself drifts. If the replay shows no step of 0.5 m, judge by find_pieces (long pieces at about 0.1 m rms = the map is fine) or fit a shorter range of scans.\n", l)
				}
			}
			return &Result{Summary: s + fx.Text("every scan placed by the rigid track"), Data: map[string]any{"rows": fx.Rows, "cloud": cloud(ss, fx.Rows), "map": p.Map}}, nil
		}})

	reg(&Tool{Name: "find_pieces", Heavy: true, Desc: "Cut a drive into stretches over which the map is rigid against the lasers, and report the step and the turn between neighbouring stretches. A large step at a seam with a turn between two long stretches means one piece of the map is attached at the wrong angle. Gives the sample ranges to use in measure_turn.",
		Params: obj(pDrive+","+pMap+","+pFromTo, "drive", "map"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, Map string
				From, To   int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			_, ch, _, r, err := c.App.follow(c, p.Drive, p.Map, p.From, p.To)
			if err != nil {
				return nil, err
			}
			ps := lasertrack.Pieces(ch, r.Rows, 45, 0)
			return &Result{Summary: p.Drive + " on " + p.Map + ":\n" + lasertrack.PiecesText(ps), Data: ps}, nil
		}})

	reg(&Tool{Name: "drift", Heavy: true, Desc: "Align the laser track to the map on sample ranges you trust and list how far the map departs from the track everywhere else (dx, dy, heading). A difference growing steadily with distance from one point = a piece attached at the wrong angle.",
		Params: obj(pDrive+","+pMap+`,"ranges":`+pRanges+","+pFromTo, "drive", "map", "ranges"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, Map string
				Ranges     [][]int
				From, To   int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			_, ch, _, r, err := c.App.follow(c, p.Drive, p.Map, p.From, p.To)
			if err != nil {
				return nil, err
			}
			rows, al, err := lasertrack.Drift(ch, r.Rows, ranges(p.Ranges), 45)
			if err != nil {
				return nil, err
			}
			every := len(rows)/40 + 1
			return &Result{Summary: p.Drive + " on " + p.Map + ":\n" + lasertrack.DriftText(rows, al, every), Data: rows}, nil
		}})

	reg(&Tool{Name: "measure_turn", Heavy: true, Desc: "Measure the rotation+shift that puts a wrongly attached piece of map back in place. pieceRanges = samples driven inside the piece (on pieceMap). refRanges = samples driven where the map is right (on refMap, default the same map). The measurement is repeated at three internal grid placements and reported with its spread; use the recommended th/tx/ty in build_move_region.",
		Params: obj(pDrive+`,"pieceMap":{"type":"string"},"pieceRanges":`+pRanges+`,"refMap":{"type":"string","description":"default: pieceMap"},"refRanges":`+pRanges+`,"stations":{"type":"array","items":{"type":"string"},"description":"stations to show the resulting move for"},`+pFromTo, "drive", "pieceMap", "pieceRanges", "refRanges"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, PieceMap, RefMap string
				PieceRanges, RefRanges  [][]int
				Stations                []string
				From, To                int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if p.RefMap == "" {
				p.RefMap = p.PieceMap
			}
			a := c.App
			ss, _, err := a.Samples(p.Drive, p.From, p.To)
			if err != nil {
				return nil, err
			}
			pm, err := a.Map(p.PieceMap)
			if err != nil {
				return nil, err
			}
			rm, err := a.Map(p.RefMap)
			if err != nil {
				return nil, err
			}
			defer func() { lasertrack.GridShift = 0 }()
			var turns []*lasertrack.Turn
			var b strings.Builder
			shifts := []float64{0, 0.013, 0.027}
			for k, sh := range shifts {
				if c.Err() != nil {
					return nil, c.Err()
				}
				lasertrack.GridShift = sh
				pr := func(d, t int) {
					c.progress(k*t+d, len(shifts)*t, fmt.Sprintf("measurement %d of %d", k+1, len(shifts)))
				}
				ch := lasertrack.Chain(ss, lasertrack.ChainOpts{SkipStill: true, Progress: pr})
				rp, err := lasertrack.Run(ss, ch, pm, lasertrack.ReplayOpts{})
				if err != nil {
					return nil, err
				}
				rr := rp
				if p.RefMap != p.PieceMap {
					if rr, err = lasertrack.Run(ss, ch, rm, lasertrack.ReplayOpts{}); err != nil {
						return nil, err
					}
				}
				ap, err := lasertrack.AlignTrack(ch, rp.Rows, ranges(p.PieceRanges), 45)
				if err != nil {
					return nil, fmt.Errorf("piece: %v", err)
				}
				ar, err := lasertrack.AlignTrack(ch, rr.Rows, ranges(p.RefRanges), 45)
				if err != nil {
					return nil, fmt.Errorf("reference: %v", err)
				}
				t := lasertrack.MeasureTurn(ap, ar)
				turns = append(turns, t)
				fmt.Fprintf(&b, "measurement %d: turn %.3f deg (piece fit rms %.3f m over %d scans, reference fit rms %.3f m over %d scans)\n", k+1, t.RotDeg, ap.RMS, ap.N, ar.RMS, ar.N)
			}
			sort.Slice(turns, func(i, j int) bool { return turns[i].RotDeg < turns[j].RotDeg })
			rec := turns[len(turns)/2]
			spread := (turns[len(turns)-1].RotDeg - turns[0].RotDeg) / 2
			fmt.Fprintf(&b, "\nRECOMMENDED (middle measurement): turn %.3f deg, spread +-%.2f deg", rec.RotDeg, spread)
			if rec.Pivot != nil {
				fmt.Fprintf(&b, ", about (%.2f, %.2f)", rec.Pivot.X, rec.Pivot.Y)
			}
			fmt.Fprintf(&b, "\n  th %.6f rad, tx %.4f, ty %.4f   (x' = cos*x - sin*y + tx)\n", rec.T.Th, rec.T.Tx, rec.T.Ty)
			if rec.Piece.RMS > 0.15 || rec.Ref.RMS > 0.15 {
				b.WriteString("WARNING: a fit above 0.15 m rms means that range is not one rigid piece; narrow the ranges (see find_pieces).\n")
			}
			if spread > 0.3 {
				b.WriteString("WARNING: the spread is large; the ranges may be too short or the recording too poor to measure this turn.\n")
			}
			b.WriteString("how far this moves stations (current position -> shift), smallest/middle/largest measurement:\n")
			want := map[string]bool{}
			for _, s := range p.Stations {
				want[strings.ToUpper(s)] = true
			}
			n := 0
			for _, s := range pm.Stations() {
				if len(want) > 0 && !want[strings.ToUpper(s.Name)] {
					continue
				}
				if len(want) == 0 {
					// without a list: show stations near the piece's samples only
					continue
				}
				var d []string
				for _, t := range turns {
					q := t.T.Apply(s.Pos)
					d = append(d, fmt.Sprintf("%.2f", math.Hypot(q.X-s.Pos.X, q.Y-s.Pos.Y)))
				}
				q := rec.T.Apply(s.Pos)
				fmt.Fprintf(&b, "  %-7s (%.2f, %.2f) -> dx %+.2f dy %+.2f   [%s m]\n", s.Name, s.Pos.X, s.Pos.Y, q.X-s.Pos.X, q.Y-s.Pos.Y, strings.Join(d, " / "))
				n++
			}
			if n == 0 {
				b.WriteString("  (pass \"stations\" to list them)\n")
			}
			return &Result{Summary: b.String(), Data: map[string]any{"th": rec.T.Th, "tx": rec.T.Tx, "ty": rec.T.Ty, "rotDeg": rec.RotDeg, "spreadDeg": spread, "pivot": rec.Pivot}}, nil
		}})

	reg(&Tool{Name: "build_move_region", Desc: "Write a candidate map: everything inside a polygon (walls, reflection points, reflectors, stations with their direction, paths, feature lines, areas, bins) moves as ONE rigid piece by a rotation+shift. Nothing inside the piece changes relative to the rest of the piece; everything outside stays identical. Optional anchor = an older map whose walls must stay where they are. The candidate is written to the candidates folder and checked automatically. It is never loaded on a robot.",
		Params: obj(`"base":{"type":"string","description":"map to start from; to keep edits made on the robot, download the robot's current map first and use that"},"name":{"type":"string","description":"file name and map name of the candidate"},"th":{"type":"number","description":"rotation in radians"},"rotDeg":{"type":"number","description":"rotation in degrees (alternative to th)"},"tx":{"type":"number"},"ty":{"type":"number"},`+pRegion+`,"anchor":{"type":"string","description":"optional older map: walls also present there stay in place"},"lines":{"type":"string","enum":["auto","all"],"description":"auto: feature lines on anchored walls stay; all: every line in the region moves"}`, "base", "name", "tx", "ty", "region"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Base, Name, Anchor, Lines string
				Th, RotDeg, Tx, Ty        float64
				Region                    [][]float64
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if p.Th == 0 {
				p.Th = p.RotDeg * math.Pi / 180
			}
			if math.Abs(p.Th) > 0.35 || math.Abs(p.Tx) > 30 || math.Abs(p.Ty) > 30 {
				return nil, fmt.Errorf("that is a turn of %.1f deg / shift of %.1f, %.1f m: too large for a map correction; check th (radians) and tx, ty", p.Th*180/math.Pi, p.Tx, p.Ty)
			}
			a := c.App
			bm, err := a.Map(p.Base)
			if err != nil {
				return nil, err
			}
			mp := mapedit.MoveParams{T: smap.Rigid{Th: p.Th, Tx: p.Tx, Ty: p.Ty}, Region: poly(p.Region), MapName: strings.TrimSuffix(p.Name, ".smap"), Lines: p.Lines}
			if p.Anchor != "" {
				if mp.Anchor, err = a.Map(p.Anchor); err != nil {
					return nil, err
				}
			}
			cand := bm.Clone()
			rep, err := mapedit.MoveRegion(cand, mp)
			if err != nil {
				return nil, err
			}
			if rep.StationsMoved == 0 && rep.WallsMoved == 0 {
				return nil, fmt.Errorf("nothing lies inside that region; no file written")
			}
			var names []string
			for n := range rep.Moved {
				names = append(names, n)
			}
			sort.Strings(names)
			extra := fmt.Sprintf("moved as one piece: turn %.3f deg, tx %.4f, ty %.4f\n  wall dots %d of %d (anchored and kept in place: %d), reflection points %d, reflectors %d\n  stations %d (largest move %.2f m at %s), paths inside %d, paths crossing the boundary %d\n  feature lines moved %d, kept on anchored walls %d; areas %v; bins %d\n  moved stations: %s\n",
				rep.RotationDeg, rep.Tx, rep.Ty, rep.WallsMoved, rep.WallsTotal, rep.WallsAnchored, rep.RssiMoved, rep.ReflectorsMoved, rep.StationsMoved, rep.LargestMove, rep.LargestMoveAt, rep.PathsInside, len(rep.PathsCrossing), rep.LinesMoved, rep.LinesKept, rep.AreasMoved, rep.BinsMoved, strings.Join(names, " "))
			s, err := a.saveCandidate(cand, p.Name, p.Base, "one region moved as a rigid piece", extra, nil)
			if err != nil {
				return nil, err
			}
			return &Result{Summary: s, Data: map[string]any{"candidate": strings.TrimSuffix(p.Name, ".smap"), "report": rep}}, nil
		}})

	reg(&Tool{Name: "build_merge_box", Desc: "Write a candidate map: inside a box, replace walls, reflection points and feature lines with those of a donor map (for example where one map's walls are thin or hand-cleaned and another's are good). Stations, paths, areas and bins are not touched.",
		Params: obj(`"base":{"type":"string"},"donor":{"type":"string"},"name":{"type":"string"},"x0":{"type":"number"},"x1":{"type":"number"},"y0":{"type":"number"},"y1":{"type":"number"}`, "base", "donor", "name", "x0", "x1", "y0", "y1"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Base, Donor, Name string
				X0, X1, Y0, Y1    float64
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			a := c.App
			bm, err := a.Map(p.Base)
			if err != nil {
				return nil, err
			}
			dm, err := a.Map(p.Donor)
			if err != nil {
				return nil, err
			}
			cand := bm.Clone()
			r := mapedit.MergeBox(cand, dm, p.X0, p.X1, p.Y0, p.Y1)
			mapedit.FixHeader(cand, strings.TrimSuffix(p.Name, ".smap"))
			extra := fmt.Sprintf("box x %.2f..%.2f, y %.2f..%.2f taken from %s:\n  wall dots removed %d, added %d; reflection points removed %d, added %d; feature lines removed %d, added %d\n", p.X0, p.X1, p.Y0, p.Y1, p.Donor, r.WallsRemoved, r.WallsAdded, r.RssiRemoved, r.RssiAdded, r.LinesRemoved, r.LinesAdded)
			s, err := a.saveCandidate(cand, p.Name, p.Base, "walls in a box taken from "+p.Donor, extra, nil)
			if err != nil {
				return nil, err
			}
			return &Result{Summary: s, Data: map[string]any{"candidate": strings.TrimSuffix(p.Name, ".smap")}}, nil
		}})

	reg(&Tool{Name: "build_remove_lines", Desc: "Write a candidate map with some feature lines removed. Give line numbers as reported by check_candidate (\"line #N\"), or fromCheck=true to remove every line the check reports as lying on a path.",
		Params: obj(`"base":{"type":"string","description":"map to edit (its line numbers are used)"},"name":{"type":"string"},"lines":{"type":"array","items":{"type":"integer"}},"fromCheck":{"type":"boolean"},"checkAgainst":{"type":"string","description":"with fromCheck: the older map to compare with"}`, "base", "name"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Base, Name, CheckAgainst string
				Lines                    []int
				FromCheck                bool
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			a := c.App
			bm, err := a.Map(p.Base)
			if err != nil {
				return nil, err
			}
			if p.FromCheck {
				if p.CheckAgainst == "" {
					return nil, fmt.Errorf("fromCheck needs checkAgainst (the map this one was built from)")
				}
				old, err := a.Map(p.CheckAgainst)
				if err != nil {
					return nil, err
				}
				for _, n := range mapedit.LinesNearPaths(old, bm, 0.45) {
					p.Lines = append(p.Lines, n.LineIdx)
				}
			}
			seen := map[int]bool{}
			uniq := p.Lines[:0]
			for _, i := range p.Lines {
				if !seen[i] {
					seen[i] = true
					uniq = append(uniq, i)
				}
			}
			p.Lines = uniq
			if len(p.Lines) == 0 {
				return nil, fmt.Errorf("no lines to remove; no file written")
			}
			cand := bm.Clone()
			n := mapedit.RemoveLines(cand, p.Lines)
			mapedit.FixHeader(cand, strings.TrimSuffix(p.Name, ".smap"))
			s, err := a.saveCandidate(cand, p.Name, p.Base, fmt.Sprintf("%d feature line(s) removed", n), fmt.Sprintf("removed feature lines (numbers in %s): %v\n", p.Base, p.Lines), nil)
			if err != nil {
				return nil, err
			}
			return &Result{Summary: s, Data: map[string]any{"candidate": strings.TrimSuffix(p.Name, ".smap")}}, nil
		}})

	reg(&Tool{Name: "robot_status", Desc: "READ-ONLY: ask a robot for its position, confidence, current station, current map and the maps stored on it.", Params: obj(`"robot":{"type":"string","description":"robot name from the workspace list"}`, "robot"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Robot string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			ip, err := c.App.robotIP(p.Robot)
			if err != nil {
				return nil, err
			}
			st, err := seer.ReadStatus(c, ip)
			if err != nil {
				return nil, fmt.Errorf("cannot read %s (%s): %v", p.Robot, ip, err)
			}
			method := "laser"
			if st.LocMethod == 1 {
				method = "reflector"
			}
			s := fmt.Sprintf("%s at %s (robot clock):\n  position (%.2f, %.2f), heading %.1f deg, confidence %.2f (%s localisation), station %s (last %s)\n  current map: %s (md5 %s)\n  maps on the robot: %s\n",
				p.Robot, st.CreateOn, st.X, st.Y, st.Angle*180/math.Pi, st.Confidence, method, st.CurrentStation, st.LastStation, st.CurrentMap, st.CurrentMapMD5, strings.Join(st.Maps, ", "))
			return &Result{Summary: s, Data: st}, nil
		}})

	reg(&Tool{Name: "robot_download_map", Desc: "READ-ONLY: copy a map file from a robot into the workspace maps folder (default: the map the robot is using now). Always start a new candidate from the robot's current file so edits made on the robot are kept.",
		Params: obj(`"robot":{"type":"string"},"mapName":{"type":"string","description":"default: the robot's current map"}`, "robot"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct{ Robot, MapName string }
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			a := c.App
			ip, err := a.robotIP(p.Robot)
			if err != nil {
				return nil, err
			}
			if p.MapName == "" {
				st, err := seer.ReadStatus(c, ip)
				if err != nil {
					return nil, fmt.Errorf("cannot read %s (%s): %v", p.Robot, ip, err)
				}
				p.MapName = st.CurrentMap
			}
			c.prog(0.2, "downloading "+p.MapName)
			b, err := seer.DownloadMap(c, ip, p.MapName)
			if err != nil {
				return nil, err
			}
			m, err := smap.FromBytes(b)
			if err != nil {
				return nil, fmt.Errorf("the robot's answer is not a map file: %v", err)
			}
			n, err := safeName(fmt.Sprintf("%s_%s_%s", p.MapName, p.Robot, time.Now().Format("20060102-1504")))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(a.ws("maps", n+".smap"), b, 0644); err != nil {
				return nil, err
			}
			return &Result{Summary: fmt.Sprintf("downloaded %q from %s -> maps\\%s.smap\n%.1f MB, md5 %s, %d stations, %d paths, %d feature lines\n", p.MapName, p.Robot, n, float64(len(b))/1e6, m.MD5, len(m.Stations()), len(m.Paths()), len(m.Lines())), Data: map[string]any{"map": n}}, nil
		}})

	reg(&Tool{Name: "record_start", Desc: "READ-ONLY: start recording position, confidence and lasers from a robot (about every 0.7 s) into a new recording. Start it before a test drive. Only reads; sends no command to the robot.",
		Params: obj(`"robot":{"type":"string"},"name":{"type":"string","description":"name of the recording"},"minutes":{"type":"integer","description":"stop automatically after this many minutes (default 30, max 240)"}`, "robot", "name"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Robot, Name string
				Minutes     int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			return c.App.recStart(p.Robot, p.Name, p.Minutes)
		}})
	reg(&Tool{Name: "record_status", Desc: "Progress of the running recording: samples so far, where the robot is, its confidence.", Params: obj(``),
		Run: func(c *Ctx, _ json.RawMessage) (*Result, error) { return c.App.recStatus(false) }})
	reg(&Tool{Name: "record_stop", Desc: "Stop the running recording.", Params: obj(``),
		Run: func(c *Ctx, _ json.RawMessage) (*Result, error) { return c.App.recStatus(true) }})
}

// ---------------------------------------------------------------- recording

type recording struct {
	robot, name string
	cancel      context.CancelFunc
	done        chan struct{}
	stats       seer.RecStats
	err         error
	started     time.Time
	phase       string // for an armed recording: waiting, recording, done
}

func (a *App) recStart(robot, name string, minutes int) (*Result, error) {
	ip, err := a.robotIP(robot)
	if err != nil {
		return nil, err
	}
	n, err := safeName(name)
	if err != nil {
		return nil, err
	}
	if minutes <= 0 {
		minutes = 30
	}
	if minutes > 240 {
		minutes = 240
	}
	a.mu.Lock()
	if a.rec != nil {
		select {
		case <-a.rec.done:
		default:
			a.mu.Unlock()
			return nil, fmt.Errorf("a recording is already running (%s on %s); stop it first", a.rec.name, a.rec.robot)
		}
	}
	a.mu.Unlock()
	dir := a.ws("drives", n)
	if lasertrack.Count(dir) > 0 {
		return nil, fmt.Errorf("a recording named %q already exists; choose another name", n)
	}
	// reach the robot once before starting, so a wrong address is reported now
	ctx0, c0 := context.WithTimeout(context.Background(), 8*time.Second)
	_, err = seer.ReadStatus(ctx0, ip)
	c0()
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s (%s): %v", robot, ip, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &recording{robot: robot, name: n, cancel: cancel, done: make(chan struct{}), started: time.Now()}
	a.mu.Lock()
	a.rec = r
	a.mu.Unlock()
	go func() {
		defer close(r.done)
		st, err := seer.Record(ctx, ip, dir, time.Duration(minutes)*time.Minute, 0, func(s seer.RecStats) {
			a.mu.Lock()
			r.stats = s
			a.mu.Unlock()
		})
		a.mu.Lock()
		r.stats.Samples, r.stats.Failed, r.err = st.Samples, st.Failed, err
		a.mu.Unlock()
	}()
	return &Result{Summary: fmt.Sprintf("recording %q started on %s for up to %d minutes (read-only). Use record_status / record_stop.", n, robot, minutes), Data: map[string]any{"drive": n}}, nil
}

func (a *App) recStatus(stop bool) (*Result, error) {
	a.mu.Lock()
	r := a.rec
	a.mu.Unlock()
	if r == nil {
		return &Result{Summary: "no recording has been started", Data: map[string]any{"running": false}}, nil
	}
	if stop {
		r.cancel()
		<-r.done
	}
	running := true
	select {
	case <-r.done:
		running = false
	default:
	}
	a.mu.Lock()
	s, phase := r.stats, r.phase
	a.mu.Unlock()
	state := "running"
	if !running {
		state = "stopped"
	}
	txt := fmt.Sprintf("recording %q on %s: %s, %d samples, %d failed reads, last %s; robot at (%.2f, %.2f) confidence %.2f %s", r.name, r.robot, state, s.Samples, s.Failed, s.Last, s.X, s.Y, s.Conf, s.Station)
	if phase != "" {
		txt = phase + "\n" + txt
	}
	if r.err != nil {
		txt += "\nerror: " + r.err.Error()
	}
	return &Result{Summary: txt, Data: map[string]any{"phase": phase, "running": running, "drive": r.name, "robot": r.robot, "stats": s, "minutes": time.Since(r.started).Minutes()}}, nil
}

// Shutdown stops a running recording cleanly.
func (a *App) Shutdown() {
	a.mu.Lock()
	r := a.rec
	a.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}

var _ = filepath.Join
