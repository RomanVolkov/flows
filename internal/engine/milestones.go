package engine

import (
	"slices"
	"sync"
	"time"
)

// MilestoneRegistry tracks named milestones reached by steps or groups.
type MilestoneRegistry struct {
	mu      sync.RWMutex
	reached map[string]int64 // key: stepID + "@" + milestone
}

// NewMilestoneRegistry creates an empty MilestoneRegistry.
func NewMilestoneRegistry() *MilestoneRegistry {
	return &MilestoneRegistry{
		reached: make(map[string]int64),
	}
}

// Publish records that stepID has reached milestone at now.
func (m *MilestoneRegistry) Publish(stepID, milestone string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reached[stepID+"@"+milestone] = now.UnixNano()
}

// IsReached reports whether stepID has reached milestone.
func (m *MilestoneRegistry) IsReached(stepID, milestone string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.reached[stepID+"@"+milestone]
	return ok
}

// EvaluateReadiness transitions waiting steps to StepReady (or StepSkipped when
// condition is false) once all prerequisite outputs and milestones are satisfied.
// Returns the IDs of steps newly transitioned to StepReady.
func EvaluateReadiness(steps map[string]*LogicalStep, edges []DependencyEdge, milestones *MilestoneRegistry) []string {
	incoming := make(map[string][]DependencyEdge)
	for _, e := range edges {
		incoming[e.ToStepID] = append(incoming[e.ToStepID], e)
	}

	var ids []string
	for id := range steps {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var newlyReady []string
	for _, id := range ids {
		st := steps[id]
		if st.Status != StepWaiting {
			continue
		}
		if st.ConditionMet != nil && !*st.ConditionMet {
			st.Status = StepSkipped
			continue
		}
		ready := true
		for _, dep := range incoming[id] {
			if dep.Milestone != "" {
				if milestones == nil || !milestones.IsReached(dep.FromStepID, dep.Milestone) {
					ready = false
					break
				}
				continue
			}
			pred, ok := steps[dep.FromStepID]
			if !ok || pred.Status != StepSucceeded {
				ready = false
				break
			}
		}
		if ready {
			st.Status = StepReady
			newlyReady = append(newlyReady, id)
		}
	}
	return newlyReady
}
