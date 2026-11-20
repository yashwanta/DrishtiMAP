// Package app is the DRISHTI Map application core: workspace folders, settings, background jobs,
// and the tool registry used by both the buttons in the window and the AI assistant.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"drishtimap/lasertrack"
	"drishtimap/smap"
)

const Version = "1.6.0"

// Build details. The release build on GitHub fills them in (-ldflags -X); a build made on a PC
// leaves them empty and the footer then says "local build".
var (
	Commit    = "" // short commit the exe was built from
	BuildDate = "" // yyyy-mm-dd
	BuiltBy   = "" // GitHub login of the person who released this version
	Changelog = "" // CHANGELOG.md, set by the main package
)

// Provider is one AI model connection.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // "anthropic" or "openai" (OpenAI-compatible: OpenAI, Azure, Gemini, Mistral, LM Studio, Ollama ...)
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	KeyEnc  string `json:"keyEnc,omitempty"` // API key, encrypted for this Windows user
	HasKey  bool   `json:"hasKey"`
}

// Config is stored in %APPDATA%\DRISHTI Map\config.json.
type Config struct {
	Workspace string     `json:"workspace"`
	Providers []Provider `json:"providers"`
	Active    string     `json:"activeProvider"`
	// AMR Health sign-ins, one per server address; the password is encrypted for this Windows user
	HealthLogins map[string]HealthLogin `json:"healthLogins,omitempty"`
}

// Robot is an entry of the workspace's robot list.
type Robot struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// App holds the running state.
type App struct {
	mu      sync.Mutex
	cfgPath string
	Cfg     Config
	Token   string

	heavy  sync.Mutex // one heavy analysis at a time
	jobs   map[string]*Job
	jobSeq int
	maps   map[string]*mapEntry
	drive  *driveEntry
	rec    *recording

	hTok, hTokURL string // AMR Health sign-in token of this run
	hTokAt        time.Time
}

type mapEntry struct {
	m   *smap.Map
	mod time.Time
	use time.Time
}

type driveEntry struct {
	key string
	ss  []*lasertrack.Sample
}

// New loads settings. defaultWS is used when no workspace was chosen yet.
func New(defaultWS string) (*App, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "DRISHTI Map")
	os.MkdirAll(dir, 0700)
	a := &App{cfgPath: filepath.Join(dir, "config.json"), jobs: map[string]*Job{}, maps: map[string]*mapEntry{}}
	if b, err := os.ReadFile(a.cfgPath); err == nil {
		json.Unmarshal(b, &a.Cfg)
	}
	if a.Cfg.Workspace == "" {
		a.Cfg.Workspace = defaultWS
	}
	if len(a.Cfg.Providers) == 0 {
		a.Cfg.Providers = []Provider{
			{ID: "claude", Name: "Claude (Anthropic)", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-opus-5-5"},
			{ID: "openai", Name: "OpenAI-compatible", Kind: "openai", BaseURL: "https://api.openai.com/v1", Model: ""},
			{ID: "ollama", Name: "Ollama (local, stays on this PC)", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: ""},
		}
		a.Cfg.Active = "claude"
	}
	tok := make([]byte, 16)
	rand.Read(tok)
	a.Token = hex.EncodeToString(tok)
	return a, a.SetWorkspace(a.Cfg.Workspace)
}

// savedWS, when set, is the workspace kept in the settings file while this run uses another one
// (started with --workspace): a one-off run must not change where the app opens next time.
var savedWS string

// UseWorkspaceOnce switches workspace for this run only.
func (a *App) UseWorkspaceOnce(dir string) error {
	keep := a.Cfg.Workspace
	if err := a.SetWorkspace(dir); err != nil {
		return err
	}
	savedWS = keep
	return a.saveCfg()
}

func (a *App) saveCfg() error {
	c := a.Cfg
	if savedWS != "" {
		c.Workspace = savedWS
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(a.cfgPath, b, 0600)
}

// SetWorkspace switches to a workspace folder and creates its subfolders.
func (a *App) SetWorkspace(dir string) error {
	dir = filepath.Clean(dir)
	for _, s := range []string{"maps", "candidates", "drives", "results"} {
		if err := os.MkdirAll(filepath.Join(dir, s), 0755); err != nil {
			return err
		}
	}
	a.mu.Lock()
	savedWS = "" // chosen on purpose: remember it
	a.Cfg.Workspace = dir
	a.maps = map[string]*mapEntry{}
	a.drive = nil
	a.mu.Unlock()
	return a.saveCfg()
}

func (a *App) ws(parts ...string) string {
	return filepath.Join(append([]string{a.Cfg.Workspace}, parts...)...)
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,90}$`)

// safeName accepts plain file names only: nothing that can leave the workspace.
func safeName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if !nameRe.MatchString(n) || strings.Contains(n, "..") {
		return "", fmt.Errorf("name %q is not allowed (letters, digits, space, . _ - ( ) + only)", n)
	}
	return n, nil
}

// FileInfo is one entry of a workspace list.
type FileInfo struct {
	Name     string  `json:"name"`
	Folder   string  `json:"folder"`
	SizeMB   float64 `json:"sizeMB,omitempty"`
	Modified string  `json:"modified"`
	Samples  int     `json:"samples,omitempty"`
}

func (a *App) listMaps(folder string) []FileInfo {
	var out []FileInfo
	es, _ := os.ReadDir(a.ws(folder))
	for _, e := range es {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".smap") {
			continue
		}
		i, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileInfo{Name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), Folder: folder, SizeMB: float64(i.Size()/10000) / 100, Modified: i.ModTime().Format("2006-01-02 15:04")})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (a *App) listDrives() []FileInfo {
	var out []FileInfo
	es, _ := os.ReadDir(a.ws("drives"))
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		i, _ := e.Info()
		f := FileInfo{Name: e.Name(), Folder: "drives", Samples: lasertrack.Count(a.ws("drives", e.Name()))}
		if i != nil {
			f.Modified = i.ModTime().Format("2006-01-02 15:04")
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Robots reads the workspace robot list.
func (a *App) Robots() []Robot {
	var r []Robot
	if b, err := os.ReadFile(a.ws("robots.json")); err == nil {
		json.Unmarshal(b, &r)
	}
	return r
}

// SaveRobots stores the workspace robot list.
func (a *App) SaveRobots(r []Robot) error {
	for _, x := range r {
		if _, err := safeName(x.Name); err != nil {
			return err
		}
		if !regexp.MustCompile(`^[A-Za-z0-9.-]{1,80}$`).MatchString(x.IP) {
			return fmt.Errorf("address %q is not valid", x.IP)
		}
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(a.ws("robots.json"), b, 0644)
}

func (a *App) robotIP(name string) (string, error) {
	for _, r := range a.Robots() {
		if strings.EqualFold(r.Name, name) {
			return r.IP, nil
		}
	}
	return "", fmt.Errorf("robot %q is not in this workspace's robot list", name)
}

// mapPath finds a map by name in maps\ or candidates\.
func (a *App) mapPath(name string) (string, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".smap")
	n, err := safeName(name)
	if err != nil {
		return "", err
	}
	for _, f := range []string{"maps", "candidates"} {
		p := a.ws(f, n+".smap")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("map %q not found in this workspace (maps or candidates)", name)
}

// Map loads a map by name, keeping the last few in memory.
func (a *App) Map(name string) (*smap.Map, error) {
	p, err := a.mapPath(name)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if e, ok := a.maps[p]; ok && e.mod.Equal(st.ModTime()) {
		e.use = time.Now()
		a.mu.Unlock()
		return e.m, nil
	}
	a.mu.Unlock()
	m, err := smap.Load(p)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.maps[p] = &mapEntry{m: m, mod: st.ModTime(), use: time.Now()}
	for len(a.maps) > 6 {
		old, ot := "", time.Now()
		for k, e := range a.maps {
			if e.use.Before(ot) {
				old, ot = k, e.use
			}
		}
		delete(a.maps, old)
	}
	a.mu.Unlock()
	return m, nil
}

func (a *App) drivePath(name string) (string, error) {
	n, err := safeName(name)
	if err != nil {
		return "", err
	}
	p := a.ws("drives", n)
	if lasertrack.Count(p) == 0 {
		return "", fmt.Errorf("recording %q not found or empty", name)
	}
	return p, nil
}

// Samples loads the laser samples of a drive (the last one stays in memory).
func (a *App) Samples(name string, from, to int) ([]*lasertrack.Sample, string, error) {
	p, err := a.drivePath(name)
	if err != nil {
		return nil, "", err
	}
	key := fmt.Sprintf("%s|%d|%d|%d", p, from, to, lasertrack.Count(p))
	a.mu.Lock()
	if a.drive != nil && a.drive.key == key {
		ss := a.drive.ss
		a.mu.Unlock()
		return ss, p, nil
	}
	a.mu.Unlock()
	ss, err := lasertrack.LoadSamples(p, from, to)
	if err != nil {
		return nil, "", err
	}
	a.mu.Lock()
	a.drive = &driveEntry{key, ss}
	a.mu.Unlock()
	return ss, p, nil
}

// ---------------------------------------------------------------- jobs

// Job is one tool run.
type Job struct {
	ID       string    `json:"id"`
	Tool     string    `json:"tool"`
	State    string    `json:"state"` // running, done, error, cancelled
	Progress float64   `json:"progress"`
	Note     string    `json:"note"`
	Error    string    `json:"error,omitempty"`
	Result   *Result   `json:"result,omitempty"`
	Started  time.Time `json:"started"`
	Seconds  float64   `json:"seconds"`
	cancel   context.CancelFunc
	done     chan struct{}
}

// Start runs a tool in the background and returns its job.
func (a *App) Start(tool string, args json.RawMessage) (*Job, error) {
	t, ok := toolByName[tool]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", tool)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.jobSeq++
	j := &Job{ID: fmt.Sprintf("j%d", a.jobSeq), Tool: tool, State: "running", Started: time.Now(), cancel: cancel, done: make(chan struct{})}
	a.jobs[j.ID] = j
	for id, o := range a.jobs { // forget old finished jobs
		if o.State != "running" && time.Since(o.Started) > 30*time.Minute {
			delete(a.jobs, id)
		}
	}
	a.mu.Unlock()
	go func() {
		defer close(j.done)
		defer cancel()
		res, err := a.runTool(ctx, t, args, func(p float64, note string) {
			a.mu.Lock()
			j.Progress, j.Note = p, note
			a.mu.Unlock()
		})
		a.mu.Lock()
		defer a.mu.Unlock()
		j.Seconds = time.Since(j.Started).Seconds()
		switch {
		case ctx.Err() != nil && err != nil:
			j.State, j.Error = "cancelled", "stopped"
		case err != nil:
			j.State, j.Error = "error", err.Error()
		default:
			j.State, j.Result, j.Progress = "done", res, 1
		}
	}()
	return j, nil
}

func (a *App) runTool(ctx context.Context, t *Tool, args json.RawMessage, prog func(float64, string)) (res *Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error in %s: %v", t.Name, r)
		}
	}()
	if t.Heavy {
		prog(0, "waiting for the previous analysis to finish")
		a.heavy.Lock()
		defer a.heavy.Unlock()
		prog(0, "")
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return t.Run(&Ctx{Context: ctx, App: a, prog: prog}, args)
}

// RunSync runs a tool and waits (used by the AI assistant).
func (a *App) RunSync(ctx context.Context, tool string, args json.RawMessage) (*Result, error) {
	j, err := a.Start(tool, args)
	if err != nil {
		return nil, err
	}
	select {
	case <-j.done:
	case <-ctx.Done():
		j.cancel()
		<-j.done
	}
	if j.State != "done" {
		return nil, fmt.Errorf("%s", j.Error)
	}
	return j.Result, nil
}

// JobView returns a copy of the job state.
func (a *App) JobView(id string) (Job, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[id]
	if !ok {
		return Job{}, false
	}
	c := *j
	if c.State == "running" {
		c.Seconds = time.Since(c.Started).Seconds()
	}
	return c, true
}

// Cancel stops a running job.
func (a *App) Cancel(id string) {
	a.mu.Lock()
	j := a.jobs[id]
	a.mu.Unlock()
	if j != nil {
		j.cancel()
	}
}
