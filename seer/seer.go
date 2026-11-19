// Package seer reads from a SEER robot's status API. It is read-only by construction:
// the only requests it can send are the ones listed in allowed, all of which only return data.
// There is no code here that loads a map, changes a parameter, relocalises or moves a robot.
package seer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Ports of the robot API used here.
const (
	PortStatus = 19204
	PortConfig = 19207
)

// allowed lists every request this program may send: port -> message type -> meaning.
var allowed = map[int]map[int]string{
	PortStatus: {
		1000: "robot information",
		1004: "position and localisation confidence",
		1009: "laser scan",
		1300: "list of maps on the robot",
	},
	PortConfig: {
		4011: "download a map file from the robot",
	},
}

// Allowed describes the requests this program can send (shown in the app's About box).
func Allowed() map[int]map[int]string { return allowed }

func frame(typ int, body []byte) []byte {
	h := make([]byte, 16, 16+len(body))
	h[0], h[1] = 0x5A, 0x01
	binary.BigEndian.PutUint16(h[2:], 1)
	binary.BigEndian.PutUint32(h[4:], uint32(len(body)))
	binary.BigEndian.PutUint16(h[8:], uint16(typ))
	return append(h, body...)
}

func ask(c net.Conn, port, typ int, body []byte, timeout time.Duration) ([]byte, error) {
	if _, ok := allowed[port][typ]; !ok {
		return nil, fmt.Errorf("request %d on port %d is not on the read-only list; refused", typ, port)
	}
	c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write(frame(typ, body)); err != nil {
		return nil, err
	}
	rh := make([]byte, 16)
	if _, err := io.ReadFull(c, rh); err != nil {
		return nil, err
	}
	if rh[0] != 0x5A {
		return nil, fmt.Errorf("unexpected answer from the robot")
	}
	n := binary.BigEndian.Uint32(rh[4:])
	if n > 200<<20 {
		return nil, fmt.Errorf("answer too large (%d bytes)", n)
	}
	data := make([]byte, n)
	_, err := io.ReadFull(c, data)
	return data, err
}

func dial(ctx context.Context, host string, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: 6 * time.Second}
	return d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
}

// Query sends one read-only request and returns the raw JSON answer.
func Query(ctx context.Context, host string, port, typ int, body []byte, timeout time.Duration) ([]byte, error) {
	if _, ok := allowed[port][typ]; !ok {
		return nil, fmt.Errorf("request %d on port %d is not on the read-only list; refused", typ, port)
	}
	c, err := dial(ctx, host, port)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return ask(c, port, typ, body, timeout)
}

// Status is the robot's position answer plus its map list.
type Status struct {
	X              float64  `json:"x"`
	Y              float64  `json:"y"`
	Angle          float64  `json:"angle"`
	Confidence     float64  `json:"confidence"`
	CurrentStation string   `json:"current_station"`
	LastStation    string   `json:"last_station"`
	LocMethod      int      `json:"loc_method"`
	LocState       int      `json:"loc_state"`
	CreateOn       string   `json:"create_on"`
	CurrentMap     string   `json:"current_map"`
	CurrentMapMD5  string   `json:"current_map_md5"`
	Maps           []string `json:"maps"`
	Model          string   `json:"model,omitempty"`
	VehicleID      string   `json:"vehicle_id,omitempty"`
}

// ReadStatus reads position, confidence and the map list.
func ReadStatus(ctx context.Context, host string) (*Status, error) {
	c, err := dial(ctx, host, PortStatus)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	st := &Status{}
	b, err := ask(c, PortStatus, 1004, nil, 6*time.Second)
	if err != nil {
		return nil, err
	}
	json.Unmarshal(b, st)
	if b, err = ask(c, PortStatus, 1300, nil, 6*time.Second); err == nil {
		json.Unmarshal(b, st)
	}
	if b, err = ask(c, PortStatus, 1000, nil, 6*time.Second); err == nil {
		json.Unmarshal(b, st)
	}
	return st, nil
}

// DownloadMap reads one map file from the robot. It does not change anything on the robot.
func DownloadMap(ctx context.Context, host, mapName string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"map_name": mapName})
	b, err := Query(ctx, host, PortConfig, 4011, body, 90*time.Second)
	if err != nil {
		return nil, err
	}
	if len(b) < 2000 {
		var e struct {
			RetCode int    `json:"ret_code"`
			ErrMsg  string `json:"err_msg"`
		}
		if json.Unmarshal(b, &e) == nil && e.RetCode != 0 {
			return nil, fmt.Errorf("robot answered: %s (code %d)", e.ErrMsg, e.RetCode)
		}
	}
	return b, nil
}

// RecStats is the progress of a recording.
type RecStats struct {
	Samples int     `json:"samples"`
	Failed  int     `json:"failedReads"`
	Last    string  `json:"last"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Conf    float64 `json:"confidence"`
	Station string  `json:"station"`
}

// Record reads pose, laser, pose about every 0.7 s into dir until ctx ends or the time is up.
// It continues numbering if dir already holds samples.
func Record(ctx context.Context, host, dir string, d time.Duration, start int, onSample func(RecStats)) (RecStats, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return RecStats{}, err
	}
	end := time.Now().Add(d)
	var c net.Conn
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	st := RecStats{Samples: start}
	firstErr := true
	for time.Now().Before(end) && ctx.Err() == nil {
		t0 := time.Now()
		if c == nil {
			var err error
			if c, err = dial(ctx, host, PortStatus); err != nil {
				c = nil
				if firstErr && st.Samples == start {
					return st, fmt.Errorf("cannot reach the robot at %s: %v", host, err)
				}
				st.Failed++
				select {
				case <-ctx.Done():
				case <-time.After(2 * time.Second):
				}
				continue
			}
			firstErr = false
		}
		a, e1 := ask(c, PortStatus, 1004, nil, 6*time.Second)
		var l, b []byte
		var e2, e3 error
		if e1 == nil {
			l, e2 = ask(c, PortStatus, 1009, nil, 6*time.Second)
		}
		if e1 == nil && e2 == nil {
			b, e3 = ask(c, PortStatus, 1004, nil, 6*time.Second)
		}
		if e1 != nil || e2 != nil || e3 != nil {
			c.Close()
			c = nil
			st.Failed++
			continue
		}
		st.Samples++
		n := fmt.Sprintf("%04d", st.Samples)
		os.WriteFile(filepath.Join(dir, "loc_"+n+".json"), a, 0644)
		os.WriteFile(filepath.Join(dir, "laser_"+n+".json"), l, 0644)
		os.WriteFile(filepath.Join(dir, "locb_"+n+".json"), b, 0644)
		st.Last = time.Now().Format("15:04:05")
		os.WriteFile(filepath.Join(dir, "count.txt"), []byte(fmt.Sprintf("%d samples, %d failed reads, last %s\n", st.Samples, st.Failed, st.Last)), 0644)
		var loc struct {
			X, Y, Confidence float64
			CurrentStation   string `json:"current_station"`
		}
		json.Unmarshal(a, &loc)
		st.X, st.Y, st.Conf, st.Station = loc.X, loc.Y, loc.Confidence, loc.CurrentStation
		if onSample != nil {
			onSample(st)
		}
		if el := time.Since(t0); el < 700*time.Millisecond {
			select {
			case <-ctx.Done():
			case <-time.After(700*time.Millisecond - el):
			}
		}
	}
	return st, nil
}

// ReadScan reads the robot's position and its current laser scan once (two read-only requests).
func ReadScan(ctx context.Context, host string) (loc, laser []byte, err error) {
	c, err := dial(ctx, host, PortStatus)
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	if loc, err = ask(c, PortStatus, 1004, nil, 6*time.Second); err != nil {
		return nil, nil, err
	}
	laser, err = ask(c, PortStatus, 1009, nil, 6*time.Second)
	return loc, laser, err
}
