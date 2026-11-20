// Package fleet reads what robots did in the past from the fleet manager's status history
// (one line per second for every robot) and, optionally, Wi-Fi events from an AMR Health server.
//
// It is READ-ONLY. The fleet manager is reached with the Windows ssh client under the user's own
// login; the only thing run there is history.py, which opens the history files for reading and
// prints text. fleet_test.go fails if that script ever gains a call that writes, deletes or runs
// another program.
package fleet

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed history.py
var script []byte

// DefaultLogDir is where a SEER RDS fleet manager keeps the per-minute robot status history.
const DefaultLogDir = "/opt/.data/rds/replay/robotStatuses"

// Source is the fleet manager to read from.
type Source struct {
	SSH    string // user@host
	LogDir string
}

var (
	sshRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,40}@[A-Za-z0-9][A-Za-z0-9.-]{0,120}$`)
	dirRe   = regexp.MustCompile(`^/[A-Za-z0-9_./-]{1,200}$`)
	robotRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,40}$`)
)

// Check validates the settings. Everything that reaches the remote command line is limited to
// letters, digits and a few plain characters, so nothing typed here can run another command.
func (s Source) Check() error {
	if !sshRe.MatchString(s.SSH) {
		return fmt.Errorf("fleet manager login %q is not valid; write it as user@address, for example reader@10.0.0.76", s.SSH)
	}
	if !dirRe.MatchString(s.dir()) || strings.Contains(s.dir(), "..") {
		return fmt.Errorf("history folder %q is not valid", s.LogDir)
	}
	return nil
}

func (s Source) dir() string {
	if strings.TrimSpace(s.LogDir) == "" {
		return DefaultLogDir
	}
	return strings.TrimRight(strings.TrimSpace(s.LogDir), "/")
}

func stamp(t time.Time) string { return t.Format("2006-01-02_15-04") }

// run sends history.py to the fleet manager and returns its output lines (without progress lines).
func (s Source) run(ctx context.Context, prog func(done, total int), args ...string) ([]string, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("the Windows OpenSSH client (ssh.exe) was not found on this PC; install it under Settings > Optional features > OpenSSH Client")
	}
	for _, a := range args {
		if !regexp.MustCompile(`^[A-Za-z0-9_./:-]{1,220}$`).MatchString(a) {
			return nil, fmt.Errorf("value %q is not allowed", a)
		}
	}
	remote := "python3 - " + strings.Join(args, " ")
	cmd := exec.CommandContext(ctx, ssh, "-o", "BatchMode=yes", "-o", "ConnectTimeout=12", "-o", "ServerAliveInterval=15", "-T", s.SSH, remote)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // no console window
	cmd.Stdin = bytes.NewReader(bytes.ReplaceAll(script, []byte("\r"), nil))            // a Windows checkout may add CRs
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var lines []string
	ended := false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "P\t"):
			f := strings.Split(l, "\t")
			if len(f) == 3 && prog != nil {
				d, _ := strconv.Atoi(f[1])
				t, _ := strconv.Atoi(f[2])
				prog(d, t)
			}
		case l == "END":
			ended = true
		default:
			lines = append(lines, l)
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if werr != nil || !ended {
		return nil, fmt.Errorf("could not read the fleet manager history from %s: %s", s.SSH, sshProblem(stderr.String(), werr))
	}
	return lines, nil
}

// sshProblem turns ssh's error output into one plain sentence (login banners are dropped).
func sshProblem(stderr string, err error) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(stderr, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "*") || strings.HasPrefix(l, "#") {
			continue
		}
		keep = append(keep, l)
	}
	msg := strings.Join(keep, " | ")
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "permission denied"):
		return "the login was refused. This PC needs an SSH key that the fleet manager accepts for that user (no password is typed here). (" + msg + ")"
	case strings.Contains(low, "host key verification failed"):
		return "this PC has not connected to that server before. Open a command prompt, run  ssh " + "user@address" + "  once and accept the server's key, then try again."
	case strings.Contains(low, "timed out") || strings.Contains(low, "could not resolve") || strings.Contains(low, "no route") || strings.Contains(low, "refused"):
		return "the server did not answer. Is this PC on the plant network (VPN)? (" + msg + ")"
	case strings.Contains(low, "python3") && strings.Contains(low, "not found"):
		return "python3 is not installed on the fleet manager."
	case msg == "" && err != nil:
		return err.Error()
	case msg == "":
		return "the reading stopped early"
	}
	if len(msg) > 400 {
		msg = msg[len(msg)-400:]
	}
	return msg
}

// Row is one second of one robot.
type Row struct {
	T       time.Time
	X, Y    float64
	Deg     float64
	Conf    float64
	Station string
	Reloc   int
	Map     string
	Conn    int
	Path    string
	Codes   []string
}

// Alarm is the moment a warning or error appeared on the robot.
type Alarm struct {
	T     time.Time
	Level string // fatals, errors, warnings
	Code  string
	Desc  string
}

// Trip is one robot's history over a time window.
type Trip struct {
	Robot    string
	From, To time.Time
	Files    int
	Rows     []Row
	Alarms   []Alarm
	Missing  int      // seconds in which the fleet manager had no data for this robot
	Vehicles []string // robots the fleet manager knows
}

const tFmt = "2006-01-02 15:04:05"

func pt(s string) time.Time { t, _ := time.ParseInLocation(tFmt, s, time.Local); return t }
func pf(s string) float64   { f, _ := strconv.ParseFloat(s, 64); return f }
func pi(s string) int       { i, _ := strconv.Atoi(s); return i }

// ParseTrip reads the lines printed by history.py in trip mode.
func ParseTrip(lines []string, robot string) *Trip {
	t := &Trip{Robot: robot}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		switch f[0] {
		case "H":
			if len(f) > 1 {
				t.Files = pi(f[1])
			}
		case "M":
			t.Missing++
		case "V":
			if len(f) > 1 {
				t.Vehicles = append(t.Vehicles, f[1])
			}
		case "E":
			if len(f) >= 5 {
				t.Alarms = append(t.Alarms, Alarm{T: pt(f[1]), Level: f[2], Code: f[3], Desc: f[4]})
			}
		case "R":
			if len(f) >= 12 {
				r := Row{T: pt(f[1]), X: pf(f[2]), Y: pf(f[3]), Deg: pf(f[4]), Conf: pf(f[5]), Station: f[6], Reloc: pi(f[7]), Map: f[8], Conn: pi(f[9]), Path: f[10]}
				if f[11] != "" {
					r.Codes = strings.Split(f[11], ",")
				}
				t.Rows = append(t.Rows, r)
			}
		}
	}
	return t
}

// Trip reads one robot's history between two times (fleet manager time).
func (s Source) Trip(ctx context.Context, robot string, from, to time.Time, prog func(done, total int)) (*Trip, error) {
	if !robotRe.MatchString(robot) {
		return nil, fmt.Errorf("robot name %q is not valid", robot)
	}
	if !to.After(from) {
		return nil, fmt.Errorf("the end time must be after the start time")
	}
	if to.Sub(from) > 6*time.Hour {
		return nil, fmt.Errorf("choose a time range of 6 hours or less (the history has one line per second)")
	}
	lines, err := s.run(ctx, prog, "trip", s.dir(), robot, stamp(from), stamp(to))
	if err != nil {
		return nil, err
	}
	t := ParseTrip(lines, robot)
	t.From, t.To = from, to
	if t.Files == 0 {
		return nil, fmt.Errorf("the fleet manager has no history files between %s and %s in %s (old files are deleted after some days; times are fleet manager time)", from.Format("2006-01-02 15:04"), to.Format("15:04"), s.dir())
	}
	if len(t.Rows) == 0 {
		return nil, fmt.Errorf("the fleet manager history has no robot named %q in that time. Robots it knows: %s", robot, strings.Join(t.Vehicles, ", "))
	}
	return t, nil
}

// Pass is one robot's drive through a box.
type Pass struct {
	T0, T1  time.Time
	Robot   string
	Seconds int
	MinConf float64
	AvgConf float64
	Still   int // seconds without moving
	Reloc   int
	Map     string
	Codes   []string // error codes seen
	DX, DY  float64  // where it left the box minus where it entered (direction of travel)
	MX, MY  float64  // where its confidence was lowest
}

// ParseLane reads the lines printed by history.py in lane mode.
func ParseLane(lines []string) []Pass {
	var out []Pass
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if f[0] != "L" || len(f) < 11 {
			continue
		}
		p := Pass{T0: pt(f[1]), Robot: f[2], T1: pt(f[3]), Seconds: pi(f[4]), MinConf: pf(f[5]), AvgConf: pf(f[6]), Still: pi(f[7]), Reloc: pi(f[8]), Map: f[9]}
		if f[10] != "" {
			p.Codes = strings.Split(f[10], ",")
		}
		if len(f) >= 15 {
			p.DX, p.DY, p.MX, p.MY = pf(f[11]), pf(f[12]), pf(f[13]), pf(f[14])
		}
		out = append(out, p)
	}
	return out
}

// Lane reads every robot's passes through a box (x0,y0,x1,y1) between two times.
func (s Source) Lane(ctx context.Context, box [4]float64, from, to time.Time, prog func(done, total int)) ([]Pass, error) {
	if to.Sub(from) > 8*24*time.Hour {
		return nil, fmt.Errorf("compare over 7 days or less")
	}
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	lines, err := s.run(ctx, prog, "lane", s.dir(), n(box[0]), n(box[1]), n(box[2]), n(box[3]), stamp(from), stamp(to), "3")
	if err != nil {
		return nil, err
	}
	return ParseLane(lines), nil
}
