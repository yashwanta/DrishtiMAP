package fleet

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Scan is one laser scan that the AMR Health server stored from a robot (the list entry; the
// robot's reply itself is fetched separately).
type Scan struct {
	ID          int64     `json:"id"`
	AMR         string    `json:"amr"`
	CapturedAt  time.Time `json:"captured_at"` // when the robot was asked for the scan
	StatusAt    time.Time `json:"status_at"`   // when the position below was reported
	Reason      string    `json:"reason"`
	Detail      string    `json:"detail"`
	Confidence  *float64  `json:"confidence"`
	X           *float64  `json:"pos_x"`
	Y           *float64  `json:"pos_y"`
	Angle       *float64  `json:"angle"`
	Station     string    `json:"station"`
	RelocStatus string    `json:"reloc_status"`
	Lasers      int       `json:"lasers"`
	Points      int       `json:"points"`
	HasPoints   bool      `json:"reply_has_points"`
}

func (h Health) client() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: h.Insecure}}}
}

func (h Health) base() string { return strings.TrimRight(strings.TrimSpace(h.URL), "/") }

func healthErr(err error) error {
	if strings.Contains(err.Error(), "certificate") {
		return fmt.Errorf("the AMR Health server's certificate is not trusted by this PC. If it is your own server, tick \"server uses its own certificate\" in History sources")
	}
	return fmt.Errorf("the AMR Health server did not answer: %v", err)
}

// Login signs in to AMR Health and returns the token for the following requests (valid about 12 hours).
// The password goes only to the server in the workspace settings, over the connection to that server.
func (h Health) Login(ctx context.Context, user, pass string) (string, error) {
	if err := h.Check(); err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(ctx, "POST", h.base()+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client().Do(req)
	if err != nil {
		return "", healthErr(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == 401:
		return "", fmt.Errorf("AMR Health did not accept that user name and password")
	case res.StatusCode == 429:
		return "", fmt.Errorf("AMR Health says: too many sign-in attempts; wait a few minutes")
	case res.StatusCode != 200:
		return "", fmt.Errorf("AMR Health answered %d to the sign-in", res.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(b, &out) != nil || out.Token == "" {
		return "", fmt.Errorf("AMR Health signed in but returned no token")
	}
	return out.Token, nil
}

func (h Health) get(ctx context.Context, token, path string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", h.base()+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := h.client().Do(req)
	if err != nil {
		return nil, 0, healthErr(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit))
	return b, res.StatusCode, err
}

// ErrSignIn means the stored sign-in is missing or no longer accepted.
var ErrSignIn = fmt.Errorf("sign in to AMR Health first")

// Scans lists the stored laser scans between two times (newest first, at most limit).
func (h Health) Scans(ctx context.Context, token, robot string, from, to time.Time, limit int) ([]Scan, error) {
	q := url.Values{"from": {from.UTC().Format(time.RFC3339)}, "to": {to.UTC().Format(time.RFC3339)}, "limit": {strconv.Itoa(limit)}}
	if h.Plant != "" {
		q.Set("plant", h.Plant)
	}
	if robot != "" {
		if !robotRe.MatchString(robot) {
			return nil, fmt.Errorf("robot name %q is not valid", robot)
		}
		q.Set("amr", robot)
	}
	b, code, err := h.get(ctx, token, "/api/amr/laser-scans?"+q.Encode(), 60<<20)
	if err != nil {
		return nil, err
	}
	switch {
	case code == 401:
		return nil, ErrSignIn
	case code == 404:
		return nil, fmt.Errorf("this AMR Health server does not store laser scans yet (it needs version 0.7.74 or newer)")
	case code != 200:
		return nil, fmt.Errorf("AMR Health answered %d to the scan list", code)
	}
	var out struct {
		Scans []Scan `json:"scans"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("the scan list from AMR Health could not be read: %v", err)
	}
	return out.Scans, nil
}

// ScanReply fetches the robot's own reply for one stored scan, exactly as the robot sent it.
func (h Health) ScanReply(ctx context.Context, token string, id int64) ([]byte, error) {
	b, code, err := h.get(ctx, token, "/api/amr/laser-scans/"+strconv.FormatInt(id, 10), 8<<20)
	if err != nil {
		return nil, err
	}
	switch {
	case code == 401:
		return nil, ErrSignIn
	case code != 200:
		return nil, fmt.Errorf("AMR Health answered %d for scan %d", code, id)
	}
	return b, nil
}
