package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"drishtimap/lasertrack"
	"drishtimap/seer"
)

// Arming several robots at once: each one is watched (position only) and recorded the first time
// it drives through the box. Nobody has to know in advance which robot will come by.

// ArmRow is one watched robot.
type ArmRow struct {
	Robot   string `json:"robot"`
	Drive   string `json:"drive"`
	Phase   string `json:"phase"` // waiting, recording, done, passed near, not reachable, stopped
	Samples int    `json:"samples"`
	Done    bool   `json:"done"`
	Entered bool   `json:"entered"`
}

type armSet struct {
	mu      sync.Mutex
	rows    []*ArmRow
	cancel  context.CancelFunc
	done    chan struct{}
	started time.Time
	box     [4]float64
}

func (s *armSet) view() ([]ArmRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ArmRow, len(s.rows))
	for i, r := range s.rows {
		out[i] = *r
	}
	running := true
	select {
	case <-s.done:
		running = false
	default:
	}
	return out, running
}

func (a *App) armAll(robots []string, box [4]float64, minutes int, stamp string) (*Result, error) {
	if len(robots) == 0 {
		return nil, fmt.Errorf("tick at least one robot")
	}
	if minutes <= 0 || minutes > 480 {
		minutes = 240
	}
	a.mu.Lock()
	if a.arms != nil {
		if _, running := a.arms.view(); running {
			a.mu.Unlock()
			return nil, fmt.Errorf("robots are already being watched for a box; stop that first")
		}
	}
	if a.rec != nil {
		select {
		case <-a.rec.done:
		default:
			a.mu.Unlock()
			return nil, fmt.Errorf("a recording is already running (%s on %s); stop it first", a.rec.name, a.rec.robot)
		}
	}
	a.mu.Unlock()
	if stamp == "" {
		stamp = time.Now().Format("20060102-150405")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	set := &armSet{cancel: cancel, done: make(chan struct{}), started: time.Now(), box: box}
	type job struct {
		row *ArmRow
		ip  string
		dir string
	}
	var jobs []job
	for _, name := range robots {
		ip, err := a.robotIP(name)
		if err != nil {
			cancel()
			return nil, err
		}
		n, err := safeName(strings.Map(func(r rune) rune {
			if r == ' ' {
				return '_'
			}
			return r
		}, name) + "_box_" + stamp)
		if err != nil {
			cancel()
			return nil, err
		}
		dir := a.ws("drives", n)
		if lasertrack.Count(dir) > 0 {
			cancel()
			return nil, fmt.Errorf("a recording named %q already exists", n)
		}
		row := &ArmRow{Robot: name, Drive: n, Phase: "waiting for it to come near the box"}
		set.rows = append(set.rows, row)
		jobs = append(jobs, job{row, ip, dir})
	}
	near := func(x, y float64) bool {
		return x >= box[0]-armMargin && x <= box[2]+armMargin && y >= box[1]-armMargin && y <= box[3]+armMargin
	}
	upd := func(r *ArmRow, f func()) { set.mu.Lock(); f(); set.mu.Unlock() }
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			misses := 0
			for { // 1. wait, reading only the position
				c1, cc := context.WithTimeout(ctx, 6*time.Second)
				st, err := seer.ReadStatus(c1, j.ip)
				cc()
				if ctx.Err() != nil {
					upd(j.row, func() { j.row.Phase, j.row.Done = "stopped before it came near the box", true })
					return
				}
				if err != nil {
					if misses++; misses == 3 {
						upd(j.row, func() { j.row.Phase = "not reachable (switched off, or not on this network); still trying" })
					}
				} else {
					if misses >= 3 {
						upd(j.row, func() { j.row.Phase = "waiting for it to come near the box" })
					}
					misses = 0
					if near(st.X, st.Y) {
						break
					}
				}
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
				}
			}
			// 2. record while it is near; stop 15 s after it has left
			upd(j.row, func() { j.row.Phase = "recording: it is at the box" })
			rctx, rcancel := context.WithCancel(ctx)
			defer rcancel()
			var away time.Time
			entered := false
			st, _ := seer.Record(rctx, j.ip, j.dir, time.Duration(minutes)*time.Minute, 0, func(s seer.RecStats) {
				upd(j.row, func() { j.row.Samples = s.Samples })
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
			upd(j.row, func() {
				j.row.Samples, j.row.Done, j.row.Entered = st.Samples, true, entered
				if entered {
					j.row.Phase = "done: it drove through the box"
				} else {
					j.row.Phase = "done: it passed near the box without driving through it"
				}
			})
		}(j)
	}
	go func() { wg.Wait(); cancel(); close(set.done) }()
	a.mu.Lock()
	a.arms = set
	a.mu.Unlock()
	sort.Strings(robots)
	return &Result{Summary: fmt.Sprintf("watching %d robot(s) for the box: %s.\nEach one is recorded the first time it comes within %.0f m of the box, and its recording stops by itself when it has left (read-only; gives up after %d minutes). Keep this program open. The robots are sent by the fleet manager as usual.", len(robots), strings.Join(robots, ", "), armMargin, minutes), Data: map[string]any{"robots": len(robots)}}, nil
}

// armStatus reports the watched robots; with stop it ends the watch (recordings made so far are kept).
func (a *App) armStatus(stop bool) (*Result, error) {
	a.mu.Lock()
	set := a.arms
	a.mu.Unlock()
	if set == nil {
		return &Result{Summary: "no robots are being watched", Data: map[string]any{"running": false, "rows": []ArmRow{}}}, nil
	}
	if stop {
		set.cancel()
		<-set.done
	}
	rows, running := set.view()
	var b strings.Builder
	state := "watching"
	if !running {
		state = "finished"
	}
	through := 0
	fmt.Fprintf(&b, "%s, started %s\n", state, set.started.Format("15:04"))
	for _, r := range rows {
		if r.Entered {
			through++
		}
		fmt.Fprintf(&b, "  %-9s %-58s %s\n", r.Robot, r.Phase, map[bool]string{true: fmt.Sprintf("%d samples -> %s", r.Samples, r.Drive), false: ""}[r.Samples > 0])
	}
	return &Result{Summary: b.String(), Data: map[string]any{"running": running, "rows": rows, "through": through}}, nil
}

func init() {
	reg(&Tool{Name: "record_arm_all", Desc: "READ-ONLY: watch several robots for one box. Each named robot is recorded the first time it comes near the box, and its recording stops by itself when it has left. Use it when it is not known which robot will drive through next: name all robots. record_arm_status shows what each one is doing; record_arm_stop ends the watch and keeps the recordings made so far. This program never moves a robot.",
		Params: obj(`"robots":{"type":"array","items":{"type":"string"},"description":"robot names from the workspace list"},"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4},"minutes":{"type":"integer","description":"give up after this many minutes (default 240)"}`, "robots", "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Robots  []string
				Box     []float64
				Minutes int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			b, err := box4(p.Box)
			if err != nil {
				return nil, err
			}
			return c.App.armAll(p.Robots, b, p.Minutes, "")
		}})
	reg(&Tool{Name: "record_arm_status", Desc: "What each watched robot is doing (waiting, recording, done) and which recordings were made.", Params: obj(``),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) { return c.App.armStatus(false) }})
	reg(&Tool{Name: "record_arm_stop", Desc: "Stop watching robots for a box. Recordings made so far are kept.", Params: obj(``),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) { return c.App.armStatus(true) }})
}
