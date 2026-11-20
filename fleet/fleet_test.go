package fleet

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The script that runs on the fleet manager must stay read-only: it may open files for reading and
// print. This test fails if it ever gains a call that writes, deletes, or starts another program.
func TestHistoryScriptIsReadOnly(t *testing.T) {
	src := string(script)
	for _, bad := range []string{"subprocess", "os.system", "os.popen", "os.remove", "os.unlink", "os.rename", "os.rmdir", "os.mkdir", "os.makedirs",
		"os.chmod", "os.chown", "os.kill", "shutil", "socket", "urllib", "requests", "exec(", "eval(", "__import__", ".write(", "truncate", "ctypes", "pty", "os.exec", "os.spawn", "os.fork"} {
		if strings.Contains(src, bad) {
			t.Errorf("history.py contains %q: the script must only read", bad)
		}
	}
	opens := regexp.MustCompile(`open\(([^)]*)\)`).FindAllStringSubmatch(src, -1)
	if len(opens) != 1 {
		t.Fatalf("history.py should open files in exactly one place, found %d", len(opens))
	}
	if !strings.Contains(opens[0][1], "'r'") {
		t.Errorf("history.py opens a file without read mode: open(%s)", opens[0][1])
	}
	if imp := regexp.MustCompile(`(?m)^import (.*)$`).FindStringSubmatch(src); imp == nil || imp[1] != "sys, os, glob, json" {
		t.Errorf("history.py imports changed: %v", imp)
	}
}

func TestSettingsCannotCarryCommands(t *testing.T) {
	for _, bad := range []string{"", "host", "-oProxyCommand=calc@host", "user@host; rm -rf /", "user@host x", "user@-host", "us er@host", "user@host`id`", "user@host$(id)"} {
		if (Source{SSH: bad}).Check() == nil {
			t.Errorf("login %q was accepted", bad)
		}
	}
	for _, bad := range []string{"relative/dir", "/opt/x; id", "/opt/$(id)", "/opt/a b", "/opt/../etc"} {
		if (Source{SSH: "reader@10.0.0.76", LogDir: bad}).Check() == nil {
			t.Errorf("folder %q was accepted", bad)
		}
	}
	if err := (Source{SSH: "reader@10.0.0.76"}).Check(); err != nil {
		t.Errorf("a plain login was refused: %v", err)
	}
	for _, bad := range []string{"ftp://x", "https://", "https://x/path", "https://x?q=1", "https://u:p@x", "x"} {
		if (Health{URL: bad}).Check() == nil {
			t.Errorf("address %q was accepted", bad)
		}
	}
}

// Real event (set DRISHTI_WS to the reference workspace): a robot that was blocked, slipped, stood
// for 21 minutes and was relocalised. The capture is the script's own output for that hour.
func TestRealTrip(t *testing.T) {
	ws := os.Getenv("DRISHTI_WS")
	if ws == "" {
		t.Skip("DRISHTI_WS not set")
	}
	b, err := os.ReadFile(filepath.Join(ws, "history", "trip_amr08_20261120.tsv"))
	if err != nil {
		t.Skip("no captured trip in the workspace")
	}
	tr := ParseTrip(strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n"), "AMR-08")
	tr.From, tr.To = pt("2026-11-20 06:20:00"), pt("2026-11-20 07:00:00")
	if len(tr.Rows) < 2300 {
		t.Fatalf("rows: %d", len(tr.Rows))
	}
	r := Analyse(tr, nil)
	var long *Stop
	for i := range r.Stops {
		if r.Stops[i].To.Sub(r.Stops[i].From) > 15*time.Minute {
			long = &r.Stops[i]
		}
	}
	if long == nil || long.X < 2.7 || long.X > 2.9 || long.Y < 60.1 || long.Y > 60.3 {
		t.Fatalf("the 21 minute stop at (2.82, 60.22) was not found: %+v", r.Stops)
	}
	if d := long.To.Sub(long.From); d < 20*time.Minute || d > 22*time.Minute {
		t.Errorf("stop length %v", d)
	}
	if r.Relocs != 1 {
		t.Errorf("relocalisations: %d, want 1", r.Relocs)
	}
	kinds := map[string]int{}
	txt := ""
	for _, e := range r.Events {
		kinds[e.Kind]++
		txt += e.Text + "\n"
	}
	for _, k := range []string{"stop", "swing", "low", "alarm", "reloc"} {
		if kinds[k] == 0 {
			t.Errorf("no %q event found", k)
		}
	}
	for _, want := range []string{"52200", "robot is blocked", "52313", "slip"} {
		if !strings.Contains(txt, want) {
			t.Errorf("timeline does not mention %q", want)
		}
	}
	if r.Gaps != 0 || r.NotConn != 0 {
		t.Errorf("connection: gaps %d, not connected %d", r.Gaps, r.NotConn)
	}
	// 06:30:22: the history stood still for 21 s while the robot drove 5 m: one connection stall
	if r.Stalled < 20 || r.Stalled > 23 || kinds["gap"] != 1 || kinds["jump"] != 0 {
		t.Errorf("stall: %d s, %d stall event(s), %d jump(s); want one 21 s stall and no jump", r.Stalled, kinds["gap"], kinds["jump"])
	}
	if r.Box == nil || r.Box[0] > 2.6 || r.Box[2] < 2.9 || r.Box[1] > 57 || r.Box[3] < 62 {
		t.Errorf("trouble area %v does not cover the lane", r.Box)
	}
	if r.MinConf > 0.2 {
		t.Errorf("lowest confidence %.2f", r.MinConf)
	}
	t.Log("\n" + r.Text())
}
