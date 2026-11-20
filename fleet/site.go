package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// What an AMR Health server offers to anyone on the plant network, without a sign-in: who the robots
// are, their alarms, when their localisation confidence was low, and when the map changed. With it a
// new user needs nothing but the server's address.

func (h Health) getOpen(ctx context.Context, path string, q url.Values, out any) error {
	if err := h.Check(); err != nil {
		return err
	}
	u := h.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	res, err := h.client().Do(req)
	if err != nil {
		return healthErr(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 80<<20))
	switch {
	case res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504:
		return errStarting
	case res.StatusCode == 401:
		return ErrSignIn
	case res.StatusCode != 200:
		return fmt.Errorf("AMR Health answered %d to %s", res.StatusCode, path)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("the answer of AMR Health to %s could not be read: %v", path, err)
	}
	return nil
}

// Info is what the server says about itself.
type Info struct {
	Version string `json:"version"`
	Plant   string `json:"site_scope"`
	Service string `json:"service"`
}

// Info asks the server which version it is and which plant it belongs to.
func (h Health) Info(ctx context.Context) (Info, error) {
	var i Info
	err := h.getOpen(ctx, "/api/health", nil, &i)
	if err == nil && i.Version == "" {
		err = fmt.Errorf("that address answers, but not as an AMR Health server")
	}
	return i, err
}

// SiteRobot is one robot as AMR Health lists it.
type SiteRobot struct {
	Name string `json:"name"`
	IP   string `json:"last_ip"`
	Live string `json:"live_status"`
}

// Robots lists the plant's robots with their addresses.
func (h Health) Robots(ctx context.Context) ([]SiteRobot, error) {
	q := url.Values{}
	if h.Plant != "" {
		q.Set("plant", h.Plant)
	}
	var r []SiteRobot
	err := h.getOpen(ctx, "/api/amr/fleet", q, &r)
	sort.Slice(r, func(i, j int) bool { return r[i].Name < r[j].Name })
	return r, err
}

// LowConf is a stretch of minutes in which a robot's confidence was below 0.5 (or its position unconfirmed).
type LowConf struct {
	AMR     string    `json:"amr"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Minutes int       `json:"minutes"`
	Lowest  *float64  `json:"lowest_confidence"`
	Station string    `json:"station"`
}

// SiteAlarm is one alarm a robot raised.
type SiteAlarm struct {
	AMR     string    `json:"amr"`
	Level   string    `json:"level"`
	Code    int       `json:"code"`
	Desc    string    `json:"description"`
	At      time.Time `json:"event_time"`
	Station string    `json:"station"`
}

// MapEvent is a change of the fleet map that the server noticed.
type MapEvent struct {
	At   time.Time `json:"event_time"`
	Kind string    `json:"kind"`
	Map  string    `json:"map_name"`
}

func (h Health) window(from, to time.Time) url.Values {
	q := url.Values{"from": {from.UTC().Format(time.RFC3339)}, "to": {to.UTC().Format(time.RFC3339)}, "limit": {"5000"}}
	if h.Plant != "" {
		q.Set("plant", h.Plant)
	}
	return q
}

// LowConfidence lists every robot's low-confidence stretches in a time range.
func (h Health) LowConfidence(ctx context.Context, from, to time.Time) ([]LowConf, error) {
	var r []LowConf
	return r, h.getOpen(ctx, "/api/amr/localization-history", h.window(from, to), &r)
}

// Alarms lists every robot's alarms in a time range.
func (h Health) Alarms(ctx context.Context, from, to time.Time) ([]SiteAlarm, error) {
	var r []SiteAlarm
	return r, h.getOpen(ctx, "/api/amr/alarm-history", h.window(from, to), &r)
}

// MapPushes lists when a map was pushed to the fleet.
func (h Health) MapPushes(ctx context.Context) ([]time.Time, error) {
	q := url.Values{}
	if h.Plant != "" {
		q.Set("plant", h.Plant)
	}
	var out struct {
		Events []MapEvent `json:"events"`
	}
	if err := h.getOpen(ctx, "/api/amr/map-events", q, &out); err != nil {
		return nil, err
	}
	var t []time.Time
	for _, e := range out.Events {
		if e.Kind == "map_pushed" {
			t = append(t, e.At)
		}
	}
	sort.Slice(t, func(i, j int) bool { return t[i].Before(t[j]) })
	return t, nil
}

// CoarseArea builds the area report from what AMR Health keeps: once-a-minute low-confidence
// stretches and alarms, each tagged with the nearest station. It is coarser than the fleet
// manager's second-by-second history: it knows where a robot had trouble, not every pass that went
// well, so it cannot say what a typical pass looks like.
func CoarseArea(low []LowConf, alarms []SiteAlarm, pushes []time.Time, inBox map[string]bool, fleetSize int, from, to time.Time, focus string) *AreaReport {
	r := &AreaReport{Robots: []RobotRow{}, Coarse: true}
	type acc struct {
		row  *RobotRow
		errs map[string]bool
	}
	by := map[string]*acc{}
	get := func(n string) *acc {
		a := by[n]
		if a == nil {
			a = &acc{row: &RobotRow{Robot: n, Worst: 2}, errs: map[string]bool{}}
			by[n] = a
		}
		return a
	}
	var when []time.Time
	for _, l := range low {
		if !inBox[l.Station] || l.Start.Before(from) || l.Start.After(to) {
			continue
		}
		a := get(l.AMR)
		c := 0.0
		if l.Lowest != nil {
			c = math.Max(0, *l.Lowest)
		}
		a.row.Passes++
		a.row.MeanMin += c
		a.row.Worst = math.Min(a.row.Worst, c)
		if c < 0.3 {
			a.row.Bad++
			r.Bad++
		} else {
			a.row.Low++
			r.Low++
		}
		if l.Minutes >= 2 {
			a.row.Stops++
			r.Events = append(r.Events, fmt.Sprintf("%s %s: low confidence for %d minutes at %s (lowest %.2f)", l.Start.Local().Format("02 Jan 15:04"), l.AMR, l.Minutes, l.Station, c))
		}
		when = append(when, l.Start)
	}
	for _, al := range alarms {
		if !inBox[al.Station] || al.Level == "warning" || al.At.Before(from) || al.At.After(to) {
			continue
		}
		a := get(al.AMR)
		k := fmt.Sprintf("%d %s", al.Code, tidy(al.Desc))
		if !a.errs[k] {
			a.errs[k] = true
			r.Events = append(r.Events, fmt.Sprintf("%s %s reported at %s: %s", al.At.Local().Format("02 Jan 15:04"), al.AMR, al.Station, k))
		}
	}
	var names []string
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	withBad, troubled := 0, 0
	for _, n := range names {
		a := by[n]
		if a.row.Passes > 0 {
			a.row.MeanMin /= float64(a.row.Passes)
			troubled++
		} else {
			a.row.Worst = 0
		}
		if a.row.Bad > 0 {
			withBad++
		}
		for e := range a.errs {
			a.row.Errors = append(a.row.Errors, e)
		}
		sort.Strings(a.row.Errors)
		r.Robots = append(r.Robots, *a.row)
	}
	sort.SliceStable(r.Robots, func(i, j int) bool { return r.Robots[i].Passes > r.Robots[j].Passes })
	r.Passes = r.Low + r.Bad
	if fleetSize < len(names) {
		fleetSize = len(names)
	}
	// since a map change?
	sort.Slice(when, func(i, j int) bool { return when[i].Before(when[j]) })
	for i := len(pushes) - 1; i >= 0; i-- {
		p := pushes[i]
		if !p.After(from) || !p.Before(to) {
			continue
		}
		before, after := 0, 0
		for _, t := range when {
			if t.Before(p) {
				before++
			} else {
				after++
			}
		}
		hb, ha := math.Max(p.Sub(from).Hours(), 0.5), math.Max(to.Sub(p).Hours(), 0.5)
		switch {
		case after >= 4 && float64(after)/ha > 2*float64(before)/hb+0.05:
			r.Patterns = append(r.Patterns, fmt.Sprintf("Map change on %s: %d low-confidence stretches in the %.0f hours before it, %d in the %.0f hours after: MORE OFTEN since then", p.Local().Format("02 Jan 15:04"), before, hb, after, ha))
		default:
			r.Patterns = append(r.Patterns, fmt.Sprintf("Map change on %s: %d low-confidence stretches before it, %d after: no clear change", p.Local().Format("02 Jan 15:04"), before, after))
		}
		break
	}
	counts := fmt.Sprintf("%d of %d robots had low confidence at a station in this box, %d times in all", troubled, fleetSize, r.Passes)
	set := func(kind, verdict, summary string, isMap bool) {
		r.Kind, r.Verdict, r.Summary, r.IsMap = kind, verdict, summary, isMap
	}
	switch {
	case len(inBox) == 0:
		r.Rating = "LOW"
		set("nodata", "No station inside the box", "This view works by station. Draw the box so that it contains the stations where the trouble is.", false)
	case r.Passes == 0:
		r.Rating = "LOW"
		set("nothing", "No low confidence recorded here", "No robot's confidence fell below 0.5 at a station in this box in that time, as far as the once-a-minute record shows. A dip shorter than a minute can be missed.", false)
	case troubled >= 4 || troubled*3 >= fleetSize:
		r.Rating = "MEDIUM"
		if troubled >= 6 && r.Passes >= 15 {
			r.Rating = "HIGH"
		}
		set("place", "The place: several robots lose confidence here", fmt.Sprintf("%d of %d robots had low confidence at stations in this box. When many robots struggle at the same place, the place is the cause: what stands there does not match the map, or there is little for the lasers to see.", troubled, fleetSize), true)
	case troubled <= 2 && r.Passes >= 3:
		r.Rating = "LOW"
		set("robot", "Mostly one robot: "+strings.Join(names, ", "), fmt.Sprintf("Only %s had low confidence here. That points at that robot or at one moment, not at the map. Check its scanners, and what happened at those times.", strings.Join(names, " and ")), false)
	default:
		r.Rating = "LOW"
		set("oneoff", "A few low-confidence moments", fmt.Sprintf("%d robot(s), %d time(s). Too few to call it the place.", troubled, r.Passes), false)
	}
	r.Why = append(r.Why, counts)
	if withBad > 0 {
		r.Why = append(r.Why, fmt.Sprintf("%d robot(s) fell below 0.3", withBad))
	}
	if focus != "" {
		if a := by[focus]; a != nil {
			r.Why = append(r.Why, fmt.Sprintf("%s: %d time(s), lowest %.2f", focus, a.row.Passes, a.row.Worst))
		} else {
			r.Why = append(r.Why, focus+": no low confidence recorded here")
		}
	}
	r.Unknown = append(r.Unknown, "How many passes went well: this record only has the troubled minutes, so it cannot say what a typical pass looks like, nor the direction of travel.")
	if r.IsMap {
		r.Unknown = append(r.Unknown, "Which walls do not match. That needs laser data: the next step.")
	}
	if len(r.Events) > 14 {
		r.Events = append(r.Events[:14], fmt.Sprintf("... %d more", len(r.Events)-14))
	}
	return r
}

// CoarseText is the plain report of a coarse area report.
func (r *AreaReport) CoarseText(box [4]float64, from, to time.Time, stations []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AREA REPORT (from AMR Health, by station)  x %.1f..%.1f  y %.1f..%.1f,  %s to %s\n", box[0], box[2], box[1], box[3], from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
	st := stations
	if len(st) > 16 {
		st = append(append([]string{}, st[:16]...), fmt.Sprintf("... %d more", len(stations)-16))
	}
	fmt.Fprintf(&b, "stations in the box: %s\n", strings.Join(st, " "))
	b.WriteString("This is the quick view: AMR Health records confidence once a minute, tagged with the nearest station. It shows where robots had trouble; it does not count the passes that went well.\n\n")
	fmt.Fprintf(&b, "VERDICT: %s   [rating %s]\n%s\n", r.Verdict, r.Rating, r.Summary)
	sec := func(t string, l []string) {
		if len(l) > 0 {
			b.WriteString("\n" + t + "\n")
			for _, x := range l {
				b.WriteString("  - " + x + "\n")
			}
		}
	}
	sec("WHY", r.Why)
	sec("PATTERNS", r.Patterns)
	sec("NOT KNOWN FROM THIS VIEW", r.Unknown)
	sec("EVENTS", r.Events)
	if len(r.Robots) > 0 {
		fmt.Fprintf(&b, "\n%-9s %-12s %7s %10s %s\n", "robot", "times low", "lowest", "below 0.3", "errors")
		for _, w := range r.Robots {
			fmt.Fprintf(&b, "%-9s %-12d %7.2f %10d %s\n", w.Robot, w.Passes, w.Worst, w.Bad, strings.Join(w.Errors, "; "))
		}
	}
	return b.String()
}
