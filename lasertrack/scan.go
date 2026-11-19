package lasertrack

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"drishtimap/smap"
)

// ScanLaser is one laser scanner in one scan: where it is mounted on the robot and what it measured.
type ScanLaser struct {
	Name     string    `json:"name"`     // device name on the robot (laser, laser2 ...)
	Model    string    `json:"model"`    // scanner type as the robot reports it
	Where    string    `json:"where"`    // front / rear / left / right, from the mounting position
	X        float64   `json:"x"`        // mounting position on the robot, metres ahead of its centre
	Y        float64   `json:"y"`        // metres to the left
	Z        float64   `json:"z"`        // height of the scan plane above the floor, metres
	HasZ     bool      `json:"hasZ"`     // the robot reported a height
	YawDeg   float64   `json:"yawDeg"`   // direction the scanner faces, relative to the robot's front
	Upside   bool      `json:"upside"`   // mounted the right way up
	MinAngle float64   `json:"minAngle"` // field of view, degrees
	MaxAngle float64   `json:"maxAngle"`
	Step     float64   `json:"step"`     // degrees between beams
	MaxRange float64   `json:"maxRange"` // metres
	Freq     float64   `json:"freq"`     // scans per second
	ForLoc   string    `json:"forLoc"`   // "yes", "no" or "" (not reported): used for localisation
	Beams    int       `json:"beams"`
	Valid    int       `json:"valid"`   // beams with a measurement
	Nearest  float64   `json:"nearest"` // metres
	Farthest float64   `json:"farthest"`
	Pts      []smap.Pt `json:"-"` // measured points in the robot frame
}

// Scan is one laser reading of a robot.
type Scan struct {
	Time   string      `json:"time"`
	Lasers []ScanLaser `json:"lasers"`
}

// ParseScan reads a robot's laser answer (status API 1009), keeping each scanner apart.
func ParseScan(b []byte) (*Scan, error) {
	var m struct {
		CreateOn string `json:"create_on"`
		Lasers   []struct {
			Beams []struct {
				Angle, Dist float64
				Valid       bool
			}
			Device struct {
				Name     string  `json:"device_name"`
				MinAngle float64 `json:"min_angle"`
				MaxAngle float64 `json:"max_angle"`
				MaxRange float64 `json:"max_range"`
				Step     float64 `json:"real_step"`
				Freq     float64 `json:"scan_freq"`
			} `json:"device_info"`
			Header struct {
				Frame string `json:"frame_id"`
			}
			Install struct {
				Upside    bool
				X, Y, Yaw float64
				Z         *float64
			} `json:"install_info"`
			ForLoc *bool `json:"use_forLoc"`
		}
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if len(m.Lasers) == 0 {
		return nil, fmt.Errorf("the answer holds no laser data")
	}
	s := &Scan{Time: m.CreateOn}
	for _, l := range m.Lasers {
		o := ScanLaser{Name: l.Device.Name, Model: strings.TrimSuffix(l.Header.Frame, "-TCP"), X: l.Install.X, Y: l.Install.Y, YawDeg: l.Install.Yaw, Upside: l.Install.Upside,
			MinAngle: l.Device.MinAngle, MaxAngle: l.Device.MaxAngle, Step: l.Device.Step, MaxRange: l.Device.MaxRange, Freq: l.Device.Freq, Beams: len(l.Beams), Nearest: math.Inf(1)}
		if l.Install.Z != nil {
			o.Z, o.HasZ = *l.Install.Z, true
		}
		if l.ForLoc != nil {
			o.ForLoc = map[bool]string{true: "yes", false: "no"}[*l.ForLoc]
		}
		switch {
		case math.Abs(o.X) >= math.Abs(o.Y) && o.X > 0.05:
			o.Where = "front"
		case math.Abs(o.X) >= math.Abs(o.Y) && o.X < -0.05:
			o.Where = "rear"
		case o.Y > 0.05:
			o.Where = "left"
		case o.Y < -0.05:
			o.Where = "right"
		default:
			o.Where = "centre"
		}
		for _, bm := range l.Beams {
			if !bm.Valid || bm.Dist <= 0 {
				continue
			}
			o.Valid++
			o.Nearest, o.Farthest = math.Min(o.Nearest, bm.Dist), math.Max(o.Farthest, bm.Dist)
			a := bm.Angle
			if !l.Install.Upside {
				a = -a
			}
			t := (a + l.Install.Yaw) * math.Pi / 180
			o.Pts = append(o.Pts, smap.Pt{X: l.Install.X + bm.Dist*math.Cos(t), Y: l.Install.Y + bm.Dist*math.Sin(t)})
		}
		if o.Valid == 0 {
			o.Nearest = 0
		}
		s.Lasers = append(s.Lasers, o)
	}
	return s, nil
}

// ReadScan reads sample i of a recording: the scan, and the pose the robot believed at that moment.
func ReadScan(dir string, i int) (*Scan, Loc, error) {
	loc, err := ReadLoc(dir, i)
	if err != nil {
		return nil, loc, fmt.Errorf("sample %d is not in this recording", i)
	}
	b, err := os.ReadFile(fname(dir, "laser", i))
	if err != nil {
		return nil, loc, fmt.Errorf("sample %d has no laser file", i)
	}
	s, err := ParseScan(b)
	return s, loc, err
}

// Text describes the scanners of a scan, one block per scanner.
func (s *Scan) Text() string {
	var b strings.Builder
	for _, l := range s.Lasers {
		h := "height not reported by the robot"
		if l.HasZ {
			h = fmt.Sprintf("scan plane %.3f m (%.0f mm) above the floor", l.Z, l.Z*1000)
		}
		fmt.Fprintf(&b, "%s (%s, %s scanner): %s\n", l.Name, l.Model, l.Where, h)
		fmt.Fprintf(&b, "    mounted %.3f m %s and %.3f m %s of the robot's centre, facing %.1f deg from straight ahead%s\n",
			math.Abs(l.X), map[bool]string{true: "ahead", false: "behind"}[l.X >= 0], math.Abs(l.Y), map[bool]string{true: "left", false: "right"}[l.Y >= 0], l.YawDeg, map[bool]string{true: "", false: ", mounted upside down"}[l.Upside])
		fmt.Fprintf(&b, "    sees %.0f to %.0f deg (%.0f deg wide), one beam every %.2f deg, up to %.0f m, %.0f scans per second\n", l.MinAngle, l.MaxAngle, l.MaxAngle-l.MinAngle, l.Step, l.MaxRange, l.Freq)
		loc := ""
		if l.ForLoc != "" {
			loc = "; used for localisation: " + l.ForLoc
		}
		fmt.Fprintf(&b, "    this scan: %d of %d beams measured something, nearest %.2f m, farthest %.2f m%s\n", l.Valid, l.Beams, l.Nearest, l.Farthest, loc)
	}
	b.WriteString("Each scanner measures in ONE flat plane at its height. It sees whatever crosses that plane (walls, rack legs, pallets, feet) and nothing above or below it: a table top, a raised fork or a hanging load above the plane is invisible to it.\n")
	return b.String()
}
