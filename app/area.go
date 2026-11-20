package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"drishtimap/fleet"
)

func init() {
	reg(&Tool{Name: "area_report", Heavy: true, Desc: "READ-ONLY: what EVERY robot did inside a box on the map over a time range, from the fleet manager's history. Tells whether a problem belongs to the place (all robots), to one robot, to that robot everywhere (its scanner), to a map change, or to one-off events, with a rating that comes from the number of robots and passes. Lists each robot's passes, lowest confidence, stops, relocalisations and errors, and patterns (direction of travel, time of day, before and after a map change). Use it first for \"is this specific to one AMR or to the location\". Give the box and either hours (back from now) or from and to. It has no laser data: when the verdict is the place, the next step is a recording through the box.",
		Params: obj(`"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"x0,y0,x1,y1 in map metres"},"hours":{"type":"number","description":"how many hours back from now (for example 6, 12, 24, 48)"},"from":{"type":"string","description":"start, fleet manager time, as 2026-01-31 06:20 (instead of hours)"},"to":{"type":"string","description":"end, same form"},"robot":{"type":"string","description":"optional: the robot the question is about"},"map":{"type":"string","description":"optional: the map whose stations are used when the report comes from AMR Health"}`, "box"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Box             []float64
				Hours           float64
				From, To, Robot string
				Map             string
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			if len(p.Box) != 4 {
				return nil, fmt.Errorf("give the box as x0,y0,x1,y1")
			}
			box := [4]float64{min(p.Box[0], p.Box[2]), min(p.Box[1], p.Box[3]), max(p.Box[0], p.Box[2]), max(p.Box[1], p.Box[3])}
			to := time.Now()
			from := to.Add(-24 * time.Hour)
			if p.Hours > 0 {
				from = to.Add(-time.Duration(p.Hours * float64(time.Hour)))
			}
			if p.From != "" {
				var err error
				if from, err = parseWhen(p.From); err != nil {
					return nil, err
				}
				if p.To != "" {
					if to, err = parseWhen(p.To); err != nil {
						return nil, err
					}
				} else {
					to = time.Now()
				}
			}
			site := c.App.Site()
			if site.FleetSSH == "" {
				// no fleet manager login: the quick view from AMR Health needs no set-up at all
				return c.App.coarseArea(c, box, from, to, p.Map, strings.TrimSpace(p.Robot))
			}
			c.prog(0, "reading every robot's history in the box")
			a, err := fleet.Source{SSH: site.FleetSSH, LogDir: site.LogDir}.Area(c, box, from, to, func(d, t int) {
				if t > 0 {
					c.prog(float64(d)/float64(t), "reading every robot's history in the box")
				}
			})
			if err != nil {
				if site.HealthURL != "" && c.Err() == nil {
					// the fleet manager cannot be read from this PC (no key yet, or not reachable): fall back
					res, err2 := c.App.coarseArea(c, box, from, to, p.Map, strings.TrimSpace(p.Robot))
					if err2 == nil {
						res.Summary = "The second-by-second history on the fleet manager could not be read from this PC:\n  " + err.Error() + "\nShowing the quick view from AMR Health instead.\n\n" + res.Summary
						return res, nil
					}
				}
				return nil, err
			}
			rep := fleet.AnalyseArea(a, strings.TrimSpace(p.Robot))
			return &Result{Summary: rep.Text(a), Data: map[string]any{"report": rep, "points": a.Points, "box": box, "from": from.Format("2006-01-02 15:04"), "to": to.Format("2006-01-02 15:04")}}, nil
		}})
}

// ---- cases: one saved investigation each (workspace\cases\<id>.json) ----

// CaseInfo is one line of the case list.
type CaseInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Step    int    `json:"step"`
	Status  string `json:"status"`
	Updated string `json:"updated"`
}

// Cases lists the saved cases, newest first.
func (a *App) Cases() []CaseInfo {
	var out []CaseInfo
	es, _ := os.ReadDir(a.ws("cases"))
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(a.ws("cases", e.Name()))
		if err != nil {
			continue
		}
		var c CaseInfo
		if json.Unmarshal(b, &c) == nil && c.ID != "" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

// Case returns one saved case as it was stored.
func (a *App) Case(id string) (json.RawMessage, error) {
	n, err := safeName(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(a.ws("cases", n+".json"))
}

// SaveCase stores a case. The body must carry id and name.
func (a *App) SaveCase(body json.RawMessage) error {
	var c CaseInfo
	if err := json.Unmarshal(body, &c); err != nil {
		return err
	}
	n, err := safeName(c.ID)
	if err != nil {
		return err
	}
	if len(body) > 4<<20 {
		return fmt.Errorf("the case is too large to save")
	}
	if err := os.MkdirAll(a.ws("cases"), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.ws("cases"), n+".json"), body, 0644)
}

// DeleteCase removes a saved case (the maps and recordings it used stay).
func (a *App) DeleteCase(id string) error {
	n, err := safeName(id)
	if err != nil {
		return err
	}
	return os.Remove(a.ws("cases", n+".json"))
}
