package nodebeat

import (
	"bytes"
	"encoding/json"
	"testing"

	"bastion.local/server/internal/managerclient"
)

func TestApplyLiveSessionsDistinguishesEmptyFromMissing(t *testing.T) {
	var legacy managerclient.HeartbeatPayload
	applyLiveSessions(&legacy, func() int { return 7 }, nil)
	if legacy.ActiveSessions != 7 {
		t.Fatalf("legacy count = %d", legacy.ActiveSessions)
	}
	if legacy.LiveSessionIDs != nil {
		t.Fatal("missing live func must leave the slice nil")
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"live_session_ids":null`)) {
		t.Fatalf("nil slice must stay null so the manager skips reconciliation: %s", raw)
	}

	var empty managerclient.HeartbeatPayload
	applyLiveSessions(&empty, func() int { return 7 }, func() []int64 { return nil })
	if empty.ActiveSessions != 0 || empty.LiveSessionIDs == nil || len(empty.LiveSessionIDs) != 0 {
		t.Fatalf("nil snapshot must become an empty list with count 0: %+v", empty)
	}
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"live_session_ids":[]`)) {
		t.Fatalf("empty live set must be an array: %s", raw)
	}

	var live managerclient.HeartbeatPayload
	applyLiveSessions(&live, func() int { return 0 }, func() []int64 { return []int64{40} })
	if live.ActiveSessions != 1 || len(live.LiveSessionIDs) != 1 || live.LiveSessionIDs[0] != 40 {
		t.Fatalf("count must follow the live set, not the tcp counter: %+v", live)
	}
}
