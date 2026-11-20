package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"drishtimap/fleet"
)

// Site holds where this workspace's plant keeps its history. It is stored in the workspace
// (site.json), next to the robot list, because it belongs to one site.
type Site struct {
	FleetSSH       string `json:"fleetSSH"`       // user@address of the fleet manager
	LogDir         string `json:"logDir"`         // empty = the usual folder
	HealthURL      string `json:"healthURL"`      // AMR Health server, optional
	Plant          string `json:"plant"`          // plant name on that server
	HealthInsecure bool   `json:"healthInsecure"` // server uses its own certificate
}

// Site reads the workspace's history settings.
func (a *App) Site() Site {
	var s Site
	if b, err := os.ReadFile(a.ws("site.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// SaveSite stores the workspace's history settings after checking them.
func (a *App) SaveSite(s Site) error {
	s.FleetSSH, s.LogDir, s.HealthURL, s.Plant = strings.TrimSpace(s.FleetSSH), strings.TrimSpace(s.LogDir), strings.TrimSpace(s.HealthURL), strings.TrimSpace(s.Plant)
	if s.FleetSSH != "" {
		if err := (fleet.Source{SSH: s.FleetSSH, LogDir: s.LogDir}).Check(); err != nil {
			return err
		}
	}
	if s.HealthURL != "" {
		if err := (fleet.Health{URL: s.HealthURL}).Check(); err != nil {
			return err
		}
	}
	if len(s.Plant) > 60 {
		return fmt.Errorf("plant name is too long")
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(a.ws("site.json"), b, 0600)
}

func parseWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "T", " "))
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("time %q is not understood; write it as 2026-01-31 06:20", s)
}

func init() {
	reg(&Tool{Name: "what_happened", Heavy: true, Desc: "READ-ONLY: find out what a robot did at a PAST time, from the fleet manager's history (one line per second): where it drove, every stop of 20 s or more, position jumps, relocalisations, heading swings without movement, low-confidence stretches, and the robot's own warnings and errors with their text. Then compares how every other robot did in the same area over the last days (is the place weak for the whole fleet, or only this robot?) and, when an AMR Health server is set up, lists the Wi-Fi events of those minutes. Use it for questions like \"why was AMR-08 off position going to LM279 this morning\". It needs the fleet manager login set up for this workspace. The history has no laser data: to see WHICH walls do not match, a drive must be recorded and replayed.",
		Params: obj(`"robot":{"type":"string","description":"robot name as the fleet manager knows it, e.g. AMR-08"},"from":{"type":"string","description":"start, fleet manager local time, as 2026-01-31 06:20"},"to":{"type":"string","description":"end, same form; at most 6 hours after from"},"map":{"type":"string","description":"optional map name from the workspace, used to name the nearest station of each place"},"compare":{"type":"boolean","description":"compare with the other robots in the same area (default true; takes about a minute per day)"},"days":{"type":"integer","description":"how many days back to compare (1 to 7, default 1)"},"wifi":{"type":"boolean","description":"include Wi-Fi events when an AMR Health server is set up (default true)"}`, "robot", "from", "to"),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Robot, From, To, Map string
				Compare, Wifi        *bool
				Days                 int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			return c.App.whatHappened(c, strings.TrimSpace(p.Robot), p.From, p.To, p.Map, p.Compare == nil || *p.Compare, p.Wifi == nil || *p.Wifi, p.Days)
		}})
}

func (a *App) whatHappened(c *Ctx, robot, fromS, toS, mapName string, compare, wifi bool, days int) (*Result, error) {
	site := a.Site()
	if site.FleetSSH == "" {
		return nil, fmt.Errorf("the fleet manager is not set up for this workspace. Press \"History sources\" (Tools tab, Past event) and enter its login as user@address")
	}
	from, err := parseWhen(fromS)
	if err != nil {
		return nil, err
	}
	to, err := parseWhen(toS)
	if err != nil {
		return nil, err
	}
	var near func(x, y float64) string
	if mapName != "" {
		if m, err := a.Map(mapName); err == nil {
			st := m.Stations()
			near = func(x, y float64) string {
				best, name := 1e9, ""
				for _, s := range st {
					if d := math.Hypot(s.Pos.X-x, s.Pos.Y-y); d < best {
						best, name = d, s.Name
					}
				}
				switch {
				case name == "" || best > 15:
					return ""
				case best < 0.3:
					return "at " + name
				}
				return fmt.Sprintf("%.1f m from %s", best, name)
			}
		}
	}
	src := fleet.Source{SSH: site.FleetSSH, LogDir: site.LogDir}
	share := 1.0
	if compare {
		share = 0.25
	}
	c.prog(0, "reading "+robot+"'s history from the fleet manager")
	trip, err := src.Trip(c, robot, from, to, func(d, t int) {
		if t > 0 {
			c.prog(share*float64(d)/float64(t), "reading "+robot+"'s history from the fleet manager")
		}
	})
	if err != nil {
		return nil, err
	}
	rep := fleet.Analyse(trip, near)
	var b strings.Builder
	b.WriteString(rep.Text())

	var cmp fleet.Compare
	compared := false
	if compare && rep.Box != nil {
		if days < 1 {
			days = 1
		}
		if days > 7 {
			days = 7
		}
		cf := to.Add(-time.Duration(days) * 24 * time.Hour)
		passes, err := src.Lane(c, *rep.Box, cf, to, func(d, t int) {
			if t > 0 {
				c.prog(share+(1-share)*float64(d)/float64(t), "comparing with the other robots in the same area")
			}
		})
		if c.Err() != nil {
			return nil, c.Err()
		}
		if err != nil {
			fmt.Fprintf(&b, "\nOTHER ROBOTS IN THE SAME AREA\n  could not be read: %v\n", err)
		} else {
			var t string
			t, cmp = fleet.LaneText(passes, robot, *rep.Box, cf, to)
			b.WriteString(t)
			compared = cmp.Others > 0
		}
	} else if compare {
		b.WriteString("\nOTHER ROBOTS IN THE SAME AREA\n  not compared: this trip shows no stop, jump or low confidence to compare\n")
	}

	if wifi && site.HealthURL != "" {
		c.prog(0.97, "reading Wi-Fi history")
		w, err := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}.Wifi(c, robot)
		if err != nil {
			fmt.Fprintf(&b, "\nWI-FI IN THE SAME MINUTES\n  could not be read: %v\n", err)
		} else {
			b.WriteString(w.Text(from, to))
		}
	}

	// in short
	b.WriteString("\nIN SHORT\n")
	n := 0
	say := func(f string, v ...any) { n++; fmt.Fprintf(&b, "  %d. "+f+"\n", append([]any{n}, v...)...) }
	for _, s := range rep.Stops {
		if s.To.Sub(s.From) < 2*time.Minute || s.Edge {
			continue
		}
		where := fmt.Sprintf("(%.2f, %.2f)", s.X, s.Y)
		if near != nil {
			where += " " + near(s.X, s.Y)
		}
		say("%s stood at %s from %s to %s.", robot, where, s.From.Format("15:04:05"), s.To.Format("15:04:05"))
	}
	var errs []string
	for _, e := range rep.Events {
		if e.Kind == "alarm" && e.Big {
			errs = append(errs, e.At+" "+strings.SplitN(e.Text, " (", 2)[0])
		}
	}
	if len(errs) > 0 {
		say("The robot itself reported: %s.", strings.Join(errs, "; "))
	}
	if rep.Relocs > 0 {
		say("It was relocalised %d time(s): somebody (or the fleet manager) had to tell it where it was.", rep.Relocs)
	}
	if rep.Distance >= 0.5 && rep.MinConf < 0.5 {
		say("Confidence fell to %.2f while driving. Below about 0.5 the lasers match the map poorly and the robot leans on its wheels to hold position.", rep.MinConf)
	}
	if compared {
		switch {
		case cmp.Weak && cmp.Worse:
			say("The place is weak for every robot (others typically fall to %.2f there), and %s went much lower (%.2f) on this trip. A weak place leaves no margin: one more thing going wrong (a blockage, a slipping wheel, something standing in the way) is enough to lose position.", cmp.Typical, robot, cmp.Mine)
		case cmp.Weak:
			say("Other robots are low in the same area too (typically %.2f), so the place is the weak spot: what stands there today no longer matches what was scanned, or there is little for the lasers to see.", cmp.Typical)
		case cmp.Marginal:
			say("The place is marginal: on a typical pass the other robots hold %.2f there, but %d of %d have had a pass below 0.35. It leaves little margin, so one more thing going wrong (a blockage, a slipping wheel, something standing in the way) is enough to lose position, as it was for %s this time.", cmp.Typical, cmp.BadPass, cmp.Others, robot)
		case cmp.Worse:
			say("Other robots keep their confidence there (typically %.2f), so it is not the place: look at %s itself or at what stood there at that moment.", cmp.Typical, robot)
		default:
			say("Other robots show about the same there (typically %.2f).", cmp.Typical)
		}
	}
	switch {
	case rep.Stalled+rep.Gaps+rep.NotConn+trip.Missing > 0:
		say("The connection to the fleet manager was not clean: %d s with no fresh data (see \"NO FRESH DATA\" and the connection section). The robot works out its position on board, so a stall does not move it off position, but it delays tasks and traffic control.", rep.Stalled+rep.Gaps+rep.NotConn+trip.Missing)
	case rep.Distance >= 0.5:
		say("No connection drop to the fleet manager was seen while the robot was driving.")
	}
	if n == 0 {
		say("Nothing unusual in this time range. If the event was at another time, change From and To (fleet manager time).")
	}
	b.WriteString("\nThis history has no laser data: it shows where and when, not which walls failed to match.\nTo see that, record a drive through the area (rec on the robot) and press Detect with Recording.\n")

	data := map[string]any{"track": rep.Track(), "events": rep.Events, "robot": robot}
	if rep.Box != nil {
		data["box"] = *rep.Box
	}
	return &Result{Summary: b.String(), Data: data}, nil
}
