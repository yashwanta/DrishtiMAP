package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"drishtimap/fleet"
	"drishtimap/seer"
	"drishtimap/smap"
)

// Joining a site: the only thing a new user needs is the address of the plant's AMR Health server.
// From it the program fills in the robots, copies the current map from a robot, and reads history,
// alarms and laser scans. The address can come from a site file next to the program, from an
// environment variable, from the company's DNS, or be typed once.

// SiteFile is what "Share this site" writes and "Join a site" reads. It holds no password.
type SiteFile struct {
	DrishtiSite    int        `json:"drishtiSite"` // format version
	Name           string     `json:"name"`
	HealthURL      string     `json:"healthURL"`
	HealthInsecure bool       `json:"healthInsecure"`
	Plant          string     `json:"plant"`
	FleetSSH       string     `json:"fleetSSH,omitempty"`  // optional: a login for the second-by-second history
	Providers      []Provider `json:"providers,omitempty"` // optional: AI connections for the Assistant, without keys
}

// ShareSite returns this workspace's site as a file to hand to a colleague.
func (a *App) ShareSite() SiteFile {
	s := a.Site()
	name := s.Plant
	if name == "" {
		name = "site"
	}
	f := SiteFile{DrishtiSite: 1, Name: name, HealthURL: s.HealthURL, HealthInsecure: s.HealthInsecure, Plant: s.Plant}
	a.mu.Lock()
	for _, p := range a.Cfg.Providers {
		if p.ID == "claude" || p.ID == "openai" || p.ID == "ollama" {
			continue // every installation has these already
		}
		p.KeyEnc, p.HasKey = "", false // a key never leaves this PC
		f.Providers = append(f.Providers, p)
	}
	a.mu.Unlock()
	return f
}

var urlish = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*(:\d+)?$`)

// JoinSite points this workspace at an AMR Health server. text is a site file's content, or just
// the server's address. The server is asked who it is before anything is saved.
func (a *App) JoinSite(ctx context.Context, text string) (fleet.Info, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\xef\xbb\xbf"))
	var f SiteFile
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &f); err != nil || f.HealthURL == "" {
			return fleet.Info{}, fmt.Errorf("that is not a DRISHTI Map site file")
		}
	} else {
		if urlish.MatchString(text) {
			text = "https://" + text
		}
		f.HealthURL = text
	}
	h := fleet.Health{URL: f.HealthURL, Insecure: f.HealthInsecure}
	if err := h.Check(); err != nil {
		return fleet.Info{}, err
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := h.Info(c)
	if err != nil && strings.Contains(err.Error(), "certificate") && !h.Insecure {
		// a plant server with its own certificate: accepted for this address, and remembered
		h.Insecure = true
		info, err = h.Info(c)
	}
	if err != nil {
		return info, err
	}
	s := a.Site()
	s.HealthURL, s.HealthInsecure, s.Plant = strings.TrimRight(f.HealthURL, "/"), h.Insecure, info.Plant
	if f.FleetSSH != "" && s.FleetSSH == "" {
		s.FleetSSH = f.FleetSSH
	}
	a.addProviders(f.Providers)
	return info, a.SaveSite(s)
}

// addProviders takes the AI connections of a site file into this user's settings: new ones are
// added, known ones get the file's address and model list. A stored key is always kept.
func (a *App) addProviders(ps []Provider) int {
	n := 0
	a.mu.Lock()
	for _, p := range ps {
		p.ID = strings.TrimSpace(p.ID)
		if _, err := safeName(p.ID); err != nil || (p.Kind != "anthropic" && p.Kind != "openai") {
			continue
		}
		if !strings.HasPrefix(p.BaseURL, "https://") && !strings.HasPrefix(p.BaseURL, "http://") {
			continue
		}
		p.KeyEnc, p.HasKey = "", false
		found := false
		for i, o := range a.Cfg.Providers {
			if o.ID == p.ID {
				found = true
				o.Name, o.BaseURL, o.Models = p.Name, strings.TrimRight(p.BaseURL, "/"), p.Models
				if o.Model == "" {
					o.Model = p.Model
				}
				a.Cfg.Providers[i] = o
			}
		}
		if !found {
			p.BaseURL = strings.TrimRight(p.BaseURL, "/")
			if p.Model == "" && len(p.Models) > 0 {
				p.Model = p.Models[0]
			}
			a.Cfg.Providers = append(a.Cfg.Providers, p)
			n++
			// make it the one in use when the current choice has no key yet
			cur := ""
			for _, o := range a.Cfg.Providers {
				if o.ID == a.Cfg.Active {
					cur = o.KeyEnc
				}
			}
			if cur == "" {
				a.Cfg.Active = p.ID
			}
		}
	}
	a.mu.Unlock()
	if len(ps) > 0 {
		a.saveCfg()
	}
	return n
}

// siteFilesBeside reads the site files that lie beside the program.
func siteFilesBeside(exeDir string) []SiteFile {
	var out []SiteFile
	for _, pat := range []string{"drishti-site.json", "*.drishti-site"} {
		m, _ := filepath.Glob(filepath.Join(exeDir, pat))
		for _, f := range m {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var sf SiteFile
			if json.Unmarshal([]byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf")), &sf) == nil {
				out = append(out, sf)
			}
		}
	}
	return out
}

// AutoSite looks, once at start-up, for a site to join when this workspace has none: a site file
// beside the program, the DRISHTI_AMRHEALTH_URL variable, or a service record in the company's DNS
// (_drishti-amrhealth._tcp). It returns what it did, in words ("" = nothing found).
func (a *App) AutoSite(exeDir string) string {
	// AI connections from a site file beside the program are taken in at every start
	for _, sf := range siteFilesBeside(exeDir) {
		a.addProviders(sf.Providers)
	}
	if a.Site().HealthURL != "" {
		return ""
	}
	try := func(text, from string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		info, err := a.JoinSite(ctx, text)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("joined %s (AMR Health %s) from %s", info.Plant, info.Version, from)
	}
	var files []string
	for _, pat := range []string{"drishti-site.json", "*.drishti-site"} {
		m, _ := filepath.Glob(filepath.Join(exeDir, pat))
		files = append(files, m...)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			if msg := try(string(b), "the site file beside the program"); msg != "" {
				return msg
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("DRISHTI_AMRHEALTH_URL")); v != "" {
		if msg := try(v, "DRISHTI_AMRHEALTH_URL"); msg != "" {
			return msg
		}
	}
	if dom := strings.TrimSpace(os.Getenv("USERDNSDOMAIN")); dom != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_, recs, err := net.DefaultResolver.LookupSRV(ctx, "drishti-amrhealth", "tcp", dom)
		cancel()
		if err == nil {
			for _, r := range recs {
				host := strings.TrimSuffix(r.Target, ".")
				if r.Port != 443 {
					host = fmt.Sprintf("%s:%d", host, r.Port)
				}
				if msg := try("https://"+host, "the company DNS"); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

// CheckRow is one line of the site checklist.
type CheckRow struct {
	Item string `json:"item"`
	OK   bool   `json:"ok"`
	Text string `json:"text"`
	Fix  string `json:"fix,omitempty"` // which button helps: join, signin, history, network
}

// siteSetup makes a joined site usable: fills in the robots, copies the fleet's map when the
// workspace has none, and reports what works. It only reads from the server and the robots.
func (a *App) siteSetup(c *Ctx) (*Result, error) {
	site := a.Site()
	var rows []CheckRow
	add := func(item string, ok bool, text, fix string) { rows = append(rows, CheckRow{item, ok, text, fix}) }
	done := func() (*Result, error) {
		var b strings.Builder
		b.WriteString("THIS SITE\n")
		for _, r := range rows {
			mark := "ok  "
			if !r.OK {
				mark = "--  "
			}
			fmt.Fprintf(&b, "  %s%-22s %s\n", mark, r.Item, r.Text)
		}
		return &Result{Summary: b.String(), Data: map[string]any{"rows": rows, "plant": site.Plant, "server": site.HealthURL}}, nil
	}
	if site.HealthURL == "" {
		add("Site", false, "not joined to a site yet. Press \"Join a site\" and give the address of the plant's AMR Health server, or open a site file.", "join")
		return done()
	}
	h := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}
	c.prog(0.05, "asking the AMR Health server")
	info, err := h.Info(c)
	if err != nil {
		add("AMR Health server", false, err.Error()+" Is this PC on the plant network (VPN)?", "network")
		return done()
	}
	if site.Plant == "" && info.Plant != "" {
		site.Plant = info.Plant
		h.Plant = info.Plant
		a.SaveSite(site)
	}
	add("AMR Health server", true, fmt.Sprintf("%s, version %s, at %s", info.Plant, info.Version, site.HealthURL), "")

	// robots: the server's list, added to what is already there
	have := a.Robots()
	if sr, err := h.Robots(c); err != nil {
		add("Robots", len(have) > 0, fmt.Sprintf("the server's robot list could not be read (%v); %d robot(s) in this workspace", err, len(have)), "")
	} else {
		known := map[string]bool{}
		for _, r := range have {
			known[strings.ToLower(r.Name)] = true
		}
		added := 0
		for _, r := range sr {
			if r.IP == "" || known[strings.ToLower(r.Name)] {
				continue
			}
			if _, err := safeName(r.Name); err != nil {
				continue
			}
			have = append(have, Robot{Name: r.Name, IP: r.IP})
			added++
		}
		if added > 0 {
			sort.Slice(have, func(i, j int) bool { return have[i].Name < have[j].Name })
			if err := a.SaveRobots(have); err != nil {
				add("Robots", false, err.Error(), "")
			}
		}
		t := fmt.Sprintf("%d robots", len(have))
		if added > 0 {
			t += fmt.Sprintf(" (%d filled in from the server)", added)
		}
		add("Robots", len(have) > 0, t, "")
	}

	// the map: copy the one the fleet runs when the workspace has none
	maps := a.listMaps("maps")
	if len(maps) == 0 {
		got := ""
		var lastErr error
		for i, r := range have {
			c.prog(0.2+0.6*float64(i)/float64(len(have)+1), "looking for a robot to copy the map from")
			ctx, cancel := context.WithTimeout(c, 8*time.Second)
			st, err := seer.ReadStatus(ctx, r.IP)
			cancel()
			if err != nil || st.CurrentMap == "" {
				lastErr = err
				continue
			}
			c.prog(0.8, "copying the map "+st.CurrentMap+" from "+r.Name)
			b, err := seer.DownloadMap(c, r.IP, st.CurrentMap)
			if err != nil {
				lastErr = err
				continue
			}
			if _, err := smap.FromBytes(b); err != nil {
				lastErr = err
				continue
			}
			n, err := safeName(st.CurrentMap + "_" + r.Name + "_" + time.Now().Format("20060102"))
			if err != nil {
				lastErr = err
				continue
			}
			if err := os.WriteFile(a.ws("maps", n+".smap"), b, 0644); err != nil {
				lastErr = err
				continue
			}
			got = n
			break
		}
		if got != "" {
			add("Map", true, "copied the fleet's map from a robot: "+got, "")
		} else {
			t := "no map yet, and no robot answered to copy it from. Is this PC on the robot network?"
			if lastErr != nil && len(have) == 0 {
				t = "no map yet and no robots to copy it from"
			}
			add("Map", false, t, "network")
		}
	} else {
		add("Map", true, fmt.Sprintf("%d in this workspace, newest %s", len(maps), newestMap(maps)), "")
	}

	// history
	if site.FleetSSH != "" {
		add("History, second by second", true, "set to "+site.FleetSSH+" (the fleet manager; needs this PC's key there)", "")
	} else {
		add("History", true, "from AMR Health, by station (the quick view). For second-by-second detail a fleet manager login can be added under History sources.", "")
	}
	// laser scans
	_, tok, err := a.scanAccess(c)
	switch {
	case err == nil && tok == "":
		add("Stored laser scans", true, "readable without a sign-in", "")
	case err == nil:
		add("Stored laser scans", true, "readable with the stored sign-in ("+a.HealthLoginUser()+")", "")
	case err == fleet.ErrSignIn:
		add("Stored laser scans", false, "this server asks for a sign-in before it gives them out. Everything else works without one.", "signin")
	default:
		add("Stored laser scans", false, err.Error(), "")
	}
	return done()
}

func newestMap(m []FileInfo) string {
	best := m[0]
	for _, f := range m {
		if f.Modified > best.Modified {
			best = f
		}
	}
	return best.Name
}

// coarseArea is the area report from AMR Health alone, for a workspace without a fleet manager login.
func (a *App) coarseArea(c *Ctx, box [4]float64, from, to time.Time, mapName, focus string) (*Result, error) {
	site := a.Site()
	if site.HealthURL == "" {
		return nil, fmt.Errorf("this workspace is not joined to a site yet. Press \"Join a site\" and give the address of the plant's AMR Health server")
	}
	if mapName == "" {
		if m := a.listMaps("maps"); len(m) > 0 {
			mapName = newestMap(m)
		}
	}
	m, err := a.Map(mapName)
	if err != nil {
		return nil, fmt.Errorf("a map is needed to know which stations are in the box: %v", err)
	}
	in := map[string]bool{}
	var names []string
	for _, s := range m.Stations() {
		if inBox(s.Pos, box) {
			in[s.Name] = true
			names = append(names, s.Name)
		}
	}
	sort.Strings(names)
	h := fleet.Health{URL: site.HealthURL, Plant: site.Plant, Insecure: site.HealthInsecure}
	c.prog(0.1, "asking AMR Health where robots lost confidence")
	low, err := h.LowConfidence(c, from, to)
	if err != nil {
		return nil, err
	}
	c.prog(0.5, "asking AMR Health for the alarms")
	alarms, _ := h.Alarms(c, from, to)
	c.prog(0.8, "asking AMR Health when the map changed")
	pushes, _ := h.MapPushes(c)
	rep := fleet.CoarseArea(low, alarms, pushes, in, len(a.Robots()), from, to, focus)
	return &Result{Summary: rep.CoarseText(box, from, to, names), Data: map[string]any{"report": rep, "points": []any{}, "box": box, "from": from.Format("2006-01-02 15:04"), "to": to.Format("2006-01-02 15:04"), "coarse": true}}, nil
}

func init() {
	reg(&Tool{Name: "site_setup", Heavy: true, Desc: "READ-ONLY towards the plant: check this workspace's site and make it usable. Reads the robot list from the plant's AMR Health server and fills in robots that are missing, copies the fleet's current map from a robot when the workspace has no map, and reports what works: the server, robots, map, history, stored laser scans. Run it when a user says the program is empty or asks what is set up.",
		Params: obj(``),
		Run:    func(c *Ctx, args json.RawMessage) (*Result, error) { return c.App.siteSetup(c) }})
}
