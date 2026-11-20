// Package server is the local web server behind the DRISHTI Map window. It listens on 127.0.0.1 only
// and every request must carry the session token, so other programs and web pages cannot use it.
package server

import (
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"drishtimap/ai"
	"drishtimap/app"
	"drishtimap/help"
	"drishtimap/mapedit"
	"drishtimap/seer"
	"drishtimap/smap"
)

//go:embed web
var webFS embed.FS

type Server struct {
	App  *app.App
	Chat *ai.Chat
	Addr string
	ln   net.Listener
}

// Start opens the listener (port 0 = any free port) and serves in the background.
func Start(a *app.App, port int) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	s := &Server{App: a, Chat: ai.New(a), Addr: ln.Addr().String(), ln: ln}
	go http.Serve(ln, s)
	return s, nil
}

// URL is the address the window opens (it carries the session token once).
func (s *Server) URL() string { return "http://" + s.Addr + "/?t=" + s.App.Token }

func (s *Server) Close() { s.ln.Close() }

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func body(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}

func (s *Server) authed(r *http.Request) bool {
	if c, err := r.Cookie("dm"); err == nil && c.Value == s.App.Token {
		return true
	}
	return r.Header.Get("X-DM-Token") == s.App.Token
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// only this PC, only by its loopback name (blocks DNS-rebinding from web pages)
	if h := r.Host; h != s.Addr && h != "localhost:"+strings.Split(s.Addr, ":")[1] {
		http.Error(w, "forbidden", 403)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	p := r.URL.Path
	if p == "/" {
		if t := r.URL.Query().Get("t"); t == s.App.Token {
			http.SetCookie(w, &http.Cookie{Name: "dm", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		} else if !s.authed(r) {
			http.Error(w, "Open DRISHTI Map from its own window.", 403)
			return
		}
		b, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'")
		w.Write(b)
		return
	}
	if !strings.HasPrefix(p, "/api/") {
		sub, _ := fs.Sub(webFS, "web")
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
		return
	}
	if !s.authed(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if r.Method == "POST" && r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+s.Addr {
		http.Error(w, "forbidden", 403)
		return
	}
	a := s.App
	switch p {
	case "/api/state":
		prov, active := a.ProvidersView()
		jsonOut(w, map[string]any{"version": app.Version, "workspace": a.Cfg.Workspace, "providers": prov, "active": active, "tools": app.Tools, "robotRequests": seer.Allowed(),
			"build": map[string]string{"commit": app.Commit, "date": app.BuildDate, "by": app.BuiltBy}})
	case "/api/changelog":
		jsonOut(w, map[string]string{"text": app.Changelog})
	case "/api/run":
		var q struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		if err := body(r, &q); err != nil {
			fail(w, 400, err)
			return
		}
		j, err := a.Start(q.Tool, q.Args)
		if err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]string{"job": j.ID})
	case "/api/job":
		j, ok := a.JobView(r.URL.Query().Get("id"))
		if !ok {
			fail(w, 404, fmt.Errorf("no such job"))
			return
		}
		if r.URL.Query().Get("brief") == "1" || j.State == "running" {
			j.Result = nil
		}
		jsonOut(w, j)
	case "/api/cancel":
		var q struct{ ID string }
		body(r, &q)
		a.Cancel(q.ID)
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/mapview":
		m, err := a.Map(r.URL.Query().Get("map"))
		if err != nil {
			fail(w, 404, err)
			return
		}
		jsonOut(w, view(m))
	case "/api/upload":
		name := strings.TrimSuffix(r.URL.Query().Get("name"), ".smap")
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,90}$`).MatchString(name) || strings.Contains(name, "..") {
			fail(w, 400, fmt.Errorf("file name %q is not allowed (letters, digits, space, . _ - ( ) + only)", name))
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 300<<20))
		if err != nil {
			fail(w, 400, err)
			return
		}
		if _, err := smap.FromBytes(b); err != nil {
			fail(w, 400, fmt.Errorf("%s is not a map file: %v", name, err))
			return
		}
		dst := filepath.Join(a.Cfg.Workspace, "maps", name+".smap")
		if _, err := os.Stat(dst); err == nil {
			fail(w, 400, fmt.Errorf("a map named %q already exists in this workspace", name))
			return
		}
		if err := os.WriteFile(dst, b, 0644); err != nil {
			fail(w, 500, err)
			return
		}
		jsonOut(w, map[string]string{"map": name})
	case "/api/workspace":
		var q struct{ Path string }
		if err := body(r, &q); err != nil || strings.TrimSpace(q.Path) == "" || !filepath.IsAbs(q.Path) {
			fail(w, 400, fmt.Errorf("give the full path of a folder, for example D:\\Maps\\Plant1"))
			return
		}
		if err := a.SetWorkspace(q.Path); err != nil {
			fail(w, 400, err)
			return
		}
		s.Chat.Reset()
		jsonOut(w, map[string]string{"workspace": a.Cfg.Workspace})
	case "/api/robots":
		var q []app.Robot
		if err := body(r, &q); err != nil {
			fail(w, 400, err)
			return
		}
		if err := a.SaveRobots(q); err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/cases":
		jsonOut(w, map[string]any{"cases": a.Cases()})
	case "/api/case":
		if r.Method == "POST" {
			b, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
			if err == nil {
				err = a.SaveCase(b)
			}
			if err != nil {
				fail(w, 400, err)
				return
			}
			jsonOut(w, map[string]bool{"ok": true})
			return
		}
		b, err := a.Case(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, 404, fmt.Errorf("no such case"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	case "/api/case/delete":
		var q struct{ ID string }
		body(r, &q)
		if err := a.DeleteCase(q.ID); err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/site":
		if r.Method == "POST" {
			var q app.Site
			if err := body(r, &q); err != nil {
				fail(w, 400, err)
				return
			}
			if err := a.SaveSite(q); err != nil {
				fail(w, 400, err)
				return
			}
		}
		jsonOut(w, a.Site())
	case "/api/provider":
		var q struct {
			Provider app.Provider `json:"provider"`
			Key      string       `json:"key"`
			Active   bool         `json:"active"`
		}
		if err := body(r, &q); err != nil {
			fail(w, 400, err)
			return
		}
		if err := a.SaveProvider(q.Provider, q.Key, q.Active); err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/provider/test":
		var q struct {
			Provider app.Provider `json:"provider"`
			Key      string       `json:"key"`
		}
		if err := body(r, &q); err != nil {
			fail(w, 400, err)
			return
		}
		key := q.Key
		if key == "" || key == "-" {
			key = a.StoredKey(q.Provider.ID)
		}
		msg, err := ai.Test(r.Context(), q.Provider, key)
		if err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]string{"message": msg})
	case "/api/provider/delete":
		var q struct{ ID string }
		body(r, &q)
		if err := a.DeleteProvider(q.ID); err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/chat/send":
		var q struct{ Text string }
		body(r, &q)
		if err := s.Chat.Send(q.Text); err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/chat/events":
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		ev, running := s.Chat.Events(after)
		jsonOut(w, map[string]any{"events": ev, "running": running})
	case "/api/chat/reset":
		s.Chat.Reset()
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/chat/stop":
		s.Chat.Stop()
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/scan":
		q := r.URL.Query()
		var v *app.ScanView
		var err error
		if rb := q.Get("robot"); rb != "" {
			v, err = a.LiveScan(r.Context(), rb)
		} else {
			i, _ := strconv.Atoi(q.Get("i"))
			v, err = a.DriveScan(q.Get("drive"), i)
		}
		if err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, v)
	case "/api/help":
		if id := r.URL.Query().Get("id"); id != "" {
			t := help.Get(id)
			if t == "" {
				fail(w, 404, fmt.Errorf("no such article"))
				return
			}
			jsonOut(w, map[string]string{"id": id, "text": t})
			return
		}
		jsonOut(w, help.List())
	case "/api/edit/state":
		editOut(w, a.EditState(), "")
	case "/api/edit/start":
		var q struct{ Base string }
		body(r, &q)
		ev, err := a.EditStart(q.Base)
		if err != nil {
			fail(w, 400, err)
			return
		}
		editOut(w, ev, "")
	case "/api/edit/op":
		var op mapedit.EditOp
		if err := body(r, &op); err != nil {
			fail(w, 400, err)
			return
		}
		ev, note, err := a.EditApply(op)
		if err != nil {
			fail(w, 400, err)
			return
		}
		editOut(w, ev, note)
	case "/api/edit/undo":
		ev, err := a.EditUndo()
		if err != nil {
			fail(w, 400, err)
			return
		}
		editOut(w, ev, "")
	case "/api/edit/cancel":
		a.EditCancel()
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/edit/save":
		var q struct{ Name string }
		body(r, &q)
		res, err := a.EditSave(q.Name)
		if err != nil {
			fail(w, 400, err)
			return
		}
		jsonOut(w, res)
	case "/api/open":
		var q struct{ What string }
		body(r, &q)
		dir := a.Cfg.Workspace
		switch q.What {
		case "maps", "candidates", "drives", "results":
			dir = filepath.Join(dir, q.What)
		}
		exec.Command("explorer.exe", dir).Start()
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/delete-candidate":
		var q struct{ Name string }
		body(r, &q)
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,90}$`).MatchString(q.Name) || strings.Contains(q.Name, "..") {
			fail(w, 400, fmt.Errorf("bad name"))
			return
		}
		// candidates only: source maps and recordings are never deleted by the app
		if err := os.Remove(filepath.Join(a.Cfg.Workspace, "candidates", q.Name+".smap")); err != nil {
			fail(w, 400, err)
			return
		}
		os.Remove(filepath.Join(a.Cfg.Workspace, "candidates", q.Name+".report.txt"))
		jsonOut(w, map[string]bool{"ok": true})
	case "/api/save":
		name := r.URL.Query().Get("name")
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+-]{0,90}\.(txt|png)$`).MatchString(name) || strings.Contains(name, "..") {
			fail(w, 400, fmt.Errorf("bad file name"))
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 60<<20))
		if err != nil {
			fail(w, 400, err)
			return
		}
		dst := filepath.Join(a.Cfg.Workspace, "results", name)
		if err := os.WriteFile(dst, b, 0644); err != nil {
			fail(w, 500, err)
			return
		}
		jsonOut(w, map[string]string{"file": dst})
	default:
		http.NotFound(w, r)
	}
}

// editOut answers with the edit in progress and its drawing data.
func editOut(w http.ResponseWriter, ev *app.EditView, note string) {
	if ev == nil {
		jsonOut(w, map[string]any{"edit": nil})
		return
	}
	jsonOut(w, map[string]any{"edit": ev, "note": note, "view": view(ev.Map)})
}

func f32(v []float64) string {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(float32(x)))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// view is the drawing data of a map.
func view(m *smap.Map) map[string]any {
	walls := make([]float64, 0, 2*len(m.Walls()))
	for _, p := range m.Walls() {
		walls = append(walls, p.X, p.Y)
	}
	type st struct {
		N string  `json:"n"`
		C string  `json:"c"`
		X float64 `json:"x"`
		Y float64 `json:"y"`
		D float64 `json:"d"`
		H bool    `json:"h"` // has a direction
		I bool    `json:"i"` // ignore direction
		P string  `json:"p"` // properties as text
	}
	var sts []st
	for _, s := range m.Stations() {
		ig, _ := s.O.Get("ignoreDir").(bool)
		sts = append(sts, st{s.Name, s.Class, s.Pos.X, s.Pos.Y, s.Dir, s.HasDir, ig, mapedit.PropText(s.O)})
	}
	var paths [][]float64
	var pnames, pinfo []string
	for _, p := range m.Paths() {
		pinfo = append(pinfo, p.Class+": "+mapedit.PropText(p.O))
		c1, c2 := p.P0, p.P3
		if p.C1 != nil && p.C2 != nil {
			c1, c2 = *p.C1, *p.C2
		}
		paths = append(paths, []float64{p.P0.X, p.P0.Y, c1.X, c1.Y, c2.X, c2.Y, p.P3.X, p.P3.Y})
		pnames = append(pnames, p.Name)
	}
	var lines []float64
	for _, l := range m.Lines() {
		lines = append(lines, l.A.X, l.A.Y, l.B.X, l.B.Y)
	}
	type area struct {
		Name string    `json:"name"`
		Pts  []float64 `json:"pts"`
	}
	var areas []area
	for _, a := range m.List("advancedAreaList") {
		ar := area{Name: a.S("className") + " " + a.S("instanceName")}
		for _, q := range smap.Objs(a.A("posGroup")) {
			ar.Pts = append(ar.Pts, q.F("x"), q.F("y"))
		}
		areas = append(areas, ar)
	}
	var refl, rssi []float64
	for _, o := range m.List("reflectorPosList") {
		refl = append(refl, o.F("x"), o.F("y"))
	}
	for _, o := range m.List("rssiPosList") {
		rssi = append(rssi, o.F("x"), o.F("y"))
	}
	mn, mx := m.Bounds()
	return map[string]any{"name": m.Name(), "md5": m.MD5, "walls": f32(walls), "stations": sts, "paths": paths, "pathNames": pnames, "pathInfo": pinfo,
		"lines": f32(lines), "areas": areas, "reflectors": refl, "rssi": f32(rssi), "bounds": []float64{mn.X, mn.Y, mx.X, mx.Y}}
}
