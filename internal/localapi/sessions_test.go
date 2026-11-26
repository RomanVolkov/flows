package localapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

func TestSessionManagerRepeatedStopAcknowledgements(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runID, err := st.CreateRun(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.AddNode(ctx, store.NewNode{RunID: runID, Kind: "session", Name: "stop-node"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := st.StartAttempt(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}

	stateDir := t.TempDir()
	mgr := NewSessionManager(st, stateDir)

	if err := mgr.AcknowledgeStop(ctx, node.ID, att.ID); err != nil {
		t.Fatalf("first AcknowledgeStop: %v", err)
	}
	if err := mgr.AcknowledgeStop(ctx, node.ID, att.ID); err != nil {
		t.Fatalf("repeated AcknowledgeStop: %v", err)
	}

	updated, err := st.Node(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.Cancelled {
		t.Fatalf("status = %s, want cancelled", updated.Status)
	}
	if got := mgr.LeaseForAttempt(att.ID); got != host.LeaseReleased {
		t.Fatalf("lease = %s, want released", got)
	}
}
