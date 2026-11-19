package seer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The robot client must refuse every request that is not on the read-only list, before any
// network connection is made. These are real SEER request numbers that change a robot.
func TestOnlyReadOnlyRequests(t *testing.T) {
	writes := []struct{ port, typ int }{
		{19205, 2002}, // relocalise
		{19205, 2022}, // switch map
		{19206, 3051}, // go to station
		{19207, 4010}, // upload a map to the robot
		{19207, 4012}, // remove a map
		{19207, 4100}, // set parameters
		{19204, 4011}, // right number, wrong port
		{19210, 6004}, // other API
	}
	for _, w := range writes {
		_, err := Query(context.Background(), "192.0.2.1", w.port, w.typ, nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), "not on the read-only list") {
			t.Errorf("request %d on port %d must be refused, got %v", w.typ, w.port, err)
		}
	}
	n := 0
	for _, m := range Allowed() {
		n += len(m)
	}
	if n != 5 {
		t.Errorf("the read-only list changed: %d entries (expected 5). Review every addition.", n)
	}
}
