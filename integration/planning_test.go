package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/workflow"
)

type taskGroupPayload struct {
	Groups []struct {
		ID         string   `json:"id"`
		Goal       string   `json:"goal"`
		Repository string   `json:"repository"`
		TicketIDs  []string `json:"ticket_ids"`
		Reason     string   `json:"reason"`
		DependsOn  []struct {
			GroupID   string `json:"group_id"`
			Milestone string `json:"milestone"`
		} `json:"depends_on"`
	} `json:"groups"`
}

func validateTaskGroupPayload(raw []byte, knownRepos map[string]bool) (*taskGroupPayload, error) {
	var p taskGroupPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if len(p.Groups) == 0 {
		return nil, os.ErrInvalid
	}
	seenTickets := make(map[string]bool)
	for _, g := range p.Groups {
		if g.ID == "" || g.Goal == "" || g.Repository == "" || g.Reason == "" || len(g.TicketIDs) == 0 {
			return nil, os.ErrInvalid
		}
		if !knownRepos[g.Repository] {
			return nil, os.ErrNotExist
		}
		for _, tid := range g.TicketIDs {
			if seenTickets[tid] {
				return nil, os.ErrExist
			}
			seenTickets[tid] = true
		}
	}
	return &p, nil
}

func TestDeliverChangePlanningAndReconciliation(t *testing.T) {
	reg := catalog.NewRegistry()
	_ = reg.RegisterBuiltins()

	def, diags := workflow.ParseFile("../workflows/deliver-change/workflow.yaml")
	if len(diags) > 0 {
		t.Fatalf("parse deliver-change: %+v", diags)
	}
	loader := func(ref string) (*workflow.Definition, error) {
		sub, subDiags := workflow.ParseFile("../workflows/deliver-change/" + ref)
		if len(subDiags) > 0 {
			return nil, fmt.Errorf("parse %s: %v", ref, subDiags)
		}
		return sub, nil
	}
	if _, cdiags := workflow.Compile(def, reg, loader); len(cdiags) > 0 {
		t.Fatalf("compile deliver-change: %+v", cdiags)
	}

	// Verify schemas/task-groups.json exists and is valid JSON.
	schemaRaw, err := os.ReadFile("../workflows/deliver-change/schemas/task-groups.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaObj map[string]any
	if err := json.Unmarshal(schemaRaw, &schemaObj); err != nil {
		t.Fatal(err)
	}

	knownRepos := map[string]bool{"repo-a": true, "repo-b": true}

	// Reject malformed payload (missing reason, unknown repo, duplicate ticket).
	badJSON := []byte(`{"groups":[{"id":"g1","goal":"x","repository":"unknown","ticket_ids":["T-1"]}]}`)
	if _, err := validateTaskGroupPayload(badJSON, knownRepos); err == nil {
		t.Fatal("expected malformed group payload to be rejected")
	}

	// Consume corrected 5-group result and materialize as one graph change.
	goodJSON := []byte(`{"groups":[
		{"id":"g1","goal":"Core API","repository":"repo-a","ticket_ids":["T-1"],"reason":"foundation"},
		{"id":"g2","goal":"Client SDK","repository":"repo-b","ticket_ids":["T-2"],"reason":"uses API","depends_on":[{"group_id":"g1","milestone":"merged"}]},
		{"id":"g3","goal":"Docs","repository":"repo-a","ticket_ids":["T-3"],"reason":"independent"},
		{"id":"g4","goal":"CLI","repository":"repo-b","ticket_ids":["T-4"],"reason":"uses SDK","depends_on":[{"group_id":"g2","milestone":"merged"}]},
		{"id":"g5","goal":"Telemetry","repository":"repo-a","ticket_ids":["T-5"],"reason":"independent"}
	]}`)
	parsed, err := validateTaskGroupPayload(goodJSON, knownRepos)
	if err != nil || len(parsed.Groups) != 5 {
		t.Fatalf("expected 5 valid groups, got err=%v", err)
	}

	decls := make([]engine.GroupDecl, 0, len(parsed.Groups))
	for _, g := range parsed.Groups {
		var deps []engine.DependencyEdge
		for _, d := range g.DependsOn {
			deps = append(deps, engine.DependencyEdge{FromStepID: d.GroupID + ".implement", Milestone: d.Milestone})
		}
		decls = append(decls, engine.GroupDecl{ID: g.ID, Name: g.Goal, DependsOn: deps})
	}
	steps, edges := engine.PublishGroups("run-plan-1", decls)
	if len(steps) != 15 {
		t.Fatalf("expected 15 steps (3 per group), got %d", len(steps))
	}
	// All 5 groups' plan steps are immediately ready before g1 merges.
	readyPlans := 0
	for _, s := range steps {
		if s.Name == "plan" && s.Status == engine.StepReady {
			readyPlans++
		}
	}
	if readyPlans != 5 {
		t.Fatalf("expected all 5 group plans ready for parallel planning, got %d", readyPlans)
	}

	// Cycle detection blocks cyclic dependency graph edits.
	gm := engine.NewGraphManager()
	gm.InitRun("run-plan-1", steps, edges)
	if _, err := gm.ApplyEdit(engine.GraphEditCommand{
		RunID:            "run-plan-1",
		ExpectedRevision: 1,
		IdempotencyKey:   "cycle-test",
		Operations: []engine.EditOperation{{
			Op:   engine.OpAddEdge,
			Edge: &engine.DependencyEdge{FromStepID: "g4.implement", ToStepID: "g1.implement"},
		}},
	}); err == nil {
		t.Fatal("expected cycle validation error on cyclic edge edit")
	}

	// Bounded plan review/fix rounds (max 3).
	next, done, needsAttn := engine.NextRepeatOccurrence("run-plan-1", "g1.plan", "review", 1, 3, false)
	if next == nil || done || needsAttn {
		t.Fatalf("expected next round at iter 1, got next=%v done=%v needsAttn=%v", next, done, needsAttn)
	}
	next, done, needsAttn = engine.NextRepeatOccurrence("run-plan-1", "g1.plan", "review", 3, 3, false)
	if next != nil || done || !needsAttn {
		t.Fatalf("expected needsAttention at max iter 3, got next=%v done=%v needsAttn=%v", next, done, needsAttn)
	}
}
