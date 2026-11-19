package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"drishtimap/lasertrack"
	"drishtimap/seer"
	"drishtimap/smap"
)

// ScanView is one laser scan placed on the map at the position the robot reported.
type ScanView struct {
	Source     string                 `json:"source"` // recording name or robot name
	Sample     int                    `json:"sample"` // 0 for a live scan
	Count      int                    `json:"count"`  // samples in the recording
	Time       string                 `json:"time"`
	X          float64                `json:"x"`
	Y          float64                `json:"y"`
	Deg        float64                `json:"deg"`
	Confidence float64                `json:"confidence"`
	Station    string                 `json:"station"`
	LocState   int                    `json:"locState"`
	Lasers     []lasertrack.ScanLaser `json:"lasers"`
	Pts        []string               `json:"pts"` // per scanner: points in map coordinates (float32 pairs, base64)
	Text       string                 `json:"text"`
}

func scanView(s *lasertrack.Scan, loc lasertrack.Loc) *ScanView {
	v := &ScanView{Time: loc.CreateOn, X: loc.X, Y: loc.Y, Deg: loc.Angle * 180 / math.Pi, Confidence: loc.Confidence, Station: loc.CurrentStation, LocState: loc.LocState, Lasers: s.Lasers}
	if v.Station == "" {
		v.Station = loc.LastStation
	}
	c, sn := math.Cos(loc.Angle), math.Sin(loc.Angle)
	for _, l := range s.Lasers {
		pts := make([]smap.Pt, len(l.Pts))
		for i, p := range l.Pts {
			pts[i] = smap.Pt{X: loc.X + c*p.X - sn*p.Y, Y: loc.Y + sn*p.X + c*p.Y}
		}
		v.Pts = append(v.Pts, f32(pts))
	}
	v.Text = fmt.Sprintf("robot at (%.2f, %.2f), heading %.1f deg, confidence %.2f, station %s, robot clock %s\n", v.X, v.Y, v.Deg, v.Confidence, v.Station, v.Time) + s.Text()
	return v
}

// DriveScan returns sample i of a recording.
func (a *App) DriveScan(drive string, i int) (*ScanView, error) {
	dir, err := a.drivePath(drive)
	if err != nil {
		return nil, err
	}
	n := lasertrack.Count(dir)
	if i < 1 {
		i = 1
	}
	if i > n {
		i = n
	}
	s, loc, err := lasertrack.ReadScan(dir, i)
	if err != nil {
		return nil, err
	}
	v := scanView(s, loc)
	v.Source, v.Sample, v.Count = drive, i, n
	v.Text = fmt.Sprintf("recording %s, sample %d of %d\n", drive, i, n) + v.Text
	return v, nil
}

// LiveScan reads a robot's current scan (read-only).
func (a *App) LiveScan(ctx context.Context, robot string) (*ScanView, error) {
	ip, err := a.robotIP(robot)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lb, sb, err := seer.ReadScan(ctx, ip)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s (%s): %v", robot, ip, err)
	}
	var loc lasertrack.Loc
	json.Unmarshal(lb, &loc)
	s, err := lasertrack.ParseScan(sb)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", robot, err)
	}
	v := scanView(s, loc)
	v.Source = robot
	v.Text = fmt.Sprintf("LIVE from %s (read-only)\n", robot) + v.Text
	return v, nil
}

func init() {
	reg(&Tool{Name: "laser_info", Desc: "The laser scanners (LiDAR) of a robot: for each one its type, mounting position, the HEIGHT of its scan plane above the floor, field of view, range, and what one scan measured. Give a recording (and optionally a sample number) or a robot name for a live read (read-only).",
		Params: obj(pDrive + `,"sample":{"type":"integer","description":"sample of the recording (default 1)"},"robot":{"type":"string","description":"robot name for a live read instead of a recording"}`),
		Run: func(c *Ctx, args json.RawMessage) (*Result, error) {
			var p struct {
				Drive, Robot string
				Sample       int
			}
			if err := parse(args, &p); err != nil {
				return nil, err
			}
			var v *ScanView
			var err error
			switch {
			case strings.TrimSpace(p.Robot) != "":
				v, err = c.App.LiveScan(c, p.Robot)
			case p.Drive != "":
				v, err = c.App.DriveScan(p.Drive, p.Sample)
			default:
				return nil, fmt.Errorf("give a recording or a robot")
			}
			if err != nil {
				return nil, err
			}
			return &Result{Summary: v.Text, Data: v}, nil
		}})
}
