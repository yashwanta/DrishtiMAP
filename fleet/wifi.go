package fleet

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Health is an AMR Health server that keeps Wi-Fi history for the robots (optional).
type Health struct {
	URL      string // https://address
	Plant    string
	Insecure bool // the server uses its own certificate: do not check it
}

type wifiFinding struct {
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Summary  string    `json:"summary"`
	First    time.Time `json:"first_seen_at"`
	Last     time.Time `json:"last_seen_at"`
	Obs      int       `json:"observations"`
}

type wifiRoam struct {
	At      time.Time `json:"at"`
	From    string    `json:"from_ap"`
	To      string    `json:"to_ap"`
	BSSID   string    `json:"to_bssid"`
	Channel string    `json:"channel"`
}

// Wifi is a robot's Wi-Fi history as the AMR Health server reports it.
type Wifi struct {
	Findings []wifiFinding `json:"findings"`
	Roams    []wifiRoam    `json:"roams"`
}

// Check validates the address: a plain http(s)://host[:port] and nothing else.
func (h Health) Check() error {
	u, err := url.Parse(strings.TrimSpace(h.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.User != nil {
		return fmt.Errorf("AMR Health address %q is not valid; write it as https://address", h.URL)
	}
	return nil
}

// Wifi reads one robot's Wi-Fi history (a read-only GET; nothing is sent but the robot and plant name).
func (h Health) Wifi(ctx context.Context, robot string) (*Wifi, error) {
	if err := h.Check(); err != nil {
		return nil, err
	}
	if !robotRe.MatchString(robot) {
		return nil, fmt.Errorf("robot name %q is not valid", robot)
	}
	q := url.Values{"amr": {robot}}
	if h.Plant != "" {
		q.Set("plant", h.Plant)
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(strings.TrimSpace(h.URL), "/")+"/api/network/history?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: h.Insecure}}}
	res, err := cl.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "certificate") {
			return nil, fmt.Errorf("the AMR Health server's certificate is not trusted by this PC. If it is your own server, tick \"server uses its own certificate\" in the history settings")
		}
		return nil, fmt.Errorf("the AMR Health server did not answer: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 30<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("the AMR Health server answered %d", res.StatusCode)
	}
	var w Wifi
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("the AMR Health server's answer could not be read: %v", err)
	}
	return &w, nil
}

// Text lists the Wi-Fi events between two times and says what they mean for a position problem.
func (w *Wifi) Text(from, to time.Time) string {
	var b strings.Builder
	b.WriteString("\nWI-FI IN THE SAME MINUTES (from the AMR Health server)\n")
	type ev struct {
		t time.Time
		s string
	}
	var evs []ev
	loss, roams := 0, 0
	var roamT []time.Time
	for _, f := range w.Findings {
		if f.Last.Before(from) || f.First.After(to) {
			continue
		}
		if f.Kind == "packet_loss" || f.Kind == "unreachable" || f.Kind == "wifi_disassociated" {
			loss++
		}
		s := f.Summary
		if f.Last.Sub(f.First) > 30*time.Second {
			s += " (until " + hms(f.Last.Local()) + ")"
		}
		evs = append(evs, ev{f.First, s})
	}
	for _, r := range w.Roams {
		if r.At.Before(from) || r.At.After(to) {
			continue
		}
		roams++
		roamT = append(roamT, r.At)
		s := "switched access point"
		if r.To != "" {
			s += " " + r.From + " -> " + r.To
			if r.Channel != "" {
				s += " (channel " + r.Channel + ")"
			}
		} else if r.BSSID != "" {
			s += " to radio " + r.BSSID
		}
		evs = append(evs, ev{r.At, s})
	}
	if len(evs) == 0 {
		b.WriteString("  no Wi-Fi event recorded for this robot in that time\n")
		return b.String()
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].t.Before(evs[j].t) })
	for i, e := range evs {
		if i == 40 {
			fmt.Fprintf(&b, "  ... %d more\n", len(evs)-i)
			break
		}
		fmt.Fprintf(&b, "  %s  %s\n", hms(e.t.Local()), e.s)
	}
	sort.Slice(roamT, func(i, j int) bool { return roamT[i].Before(roamT[j]) })
	burst := 0
	for i := range roamT {
		n := 0
		for j := i; j < len(roamT) && roamT[j].Sub(roamT[i]) <= 2*time.Minute; j++ {
			n++
		}
		if n > burst {
			burst = n
		}
	}
	fmt.Fprintf(&b, "  %d access point switches", roams)
	if burst >= 4 {
		fmt.Fprintf(&b, " (%d of them within 2 minutes: the robot was bouncing between access points)", burst)
	}
	fmt.Fprintf(&b, ", %d loss or drop warnings.\n", loss)
	b.WriteString("  A robot works out its position on board from its own lasers and map; that does not go over Wi-Fi.\n  Wi-Fi trouble shows as late task updates and traffic waits, not as a robot being off position.\n")
	return b.String()
}
