package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/workflow"
)

func waitForWorkflowTime(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func positiveDuration(value any, label string) (time.Duration, error) {
	text, _ := value.(string)
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 30s", label)
	}
	return duration, nil
}

func (r *workflowRunner) executeWait(ctx context.Context, step store.WorkflowStep, with map[string]any) (map[string]any, error) {
	value := with["duration"]
	if value == nil {
		value = with["interval"]
	}
	duration, err := positiveDuration(value, "wait duration")
	if err != nil {
		return nil, err
	}
	watch, err := r.watches.StartWatch(step.NodeID, duration, time.Now())
	if err != nil {
		return nil, err
	}
	if err := waitForWorkflowTime(ctx, time.Until(time.Unix(0, watch.DeadlineUnixNano))); err != nil {
		_ = r.watches.Cancel(step.NodeID)
		return nil, err
	}
	_, err = r.watches.RecordPoll(step.NodeID, watch.Ordinal, engine.ObservationResult{Done: true}, duration, time.Now())
	return map[string]any{"elapsed": duration.String()}, err
}

func (r *workflowRunner) executeWatch(ctx context.Context, run store.WorkflowRun, step store.WorkflowStep,
	node workflow.NodeSpec, attempt store.Attempt, snapshot *workflow.LaunchSnapshot, with map[string]any) (map[string]any, error) {
	rule := node.Watch
	interval, err := positiveDuration(rule.Interval, "watch interval")
	if err != nil {
		return nil, err
	}
	deadline, err := positiveDuration(rule.Deadline, "watch deadline")
	if err != nil {
		return nil, err
	}
	watch, err := r.watches.StartWatch(step.NodeID, deadline, time.Now())
	if err != nil {
		return nil, err
	}
	watch.Cursor = rule.Cursor
	deadlineAt := time.Unix(0, watch.DeadlineUnixNano)
	ctx, stop := context.WithDeadline(ctx, deadlineAt)
	defer stop()
	defer func() {
		if watch.TerminalState == "" {
			if time.Now().Compare(deadlineAt) >= 0 {
				_, _ = r.watches.RecordPoll(step.NodeID, watch.Ordinal, engine.ObservationResult{Cursor: watch.Cursor, Data: watch.LastObservation}, interval, time.Now())
			} else {
				_ = r.watches.Cancel(step.NodeID)
			}
		}
	}()
	for {
		if err := waitForWorkflowTime(ctx, time.Until(time.Unix(0, watch.NextWakeUnixNano))); err != nil {
			return nil, fmt.Errorf("watch stopped before its condition was met: %w", err)
		}
		poll := step
		poll.InvocationID = fmt.Sprintf("%s-%d", step.InvocationID, watch.Ordinal)
		result, err := r.invokePinnedPlugin(ctx, run, poll, node, attempt, snapshot, with, watch.Cursor)
		if err != nil {
			return nil, err
		}
		observation := result.Observation
		if observation == nil {
			return nil, fmt.Errorf("watch operation %q must return an observation", node.Uses)
		}
		data := make(map[string]any)
		if len(observation.Data) > 0 {
			if err := json.Unmarshal(observation.Data, &data); err != nil {
				return nil, fmt.Errorf("invalid watch observation: %w", err)
			}
		}
		facts := make(map[string]any, len(data)+2)
		for key, value := range data {
			facts[key] = value
		}
		facts["done"], facts["cursor"] = observation.Done, observation.Cursor
		done, err := workflowPredicate(rule.Until, workflow.BindContext{CurrentOutputs: facts})
		if err != nil {
			return nil, err
		}
		watch, err = r.watches.RecordPoll(step.NodeID, watch.Ordinal, engine.ObservationResult{
			Done: done, Cursor: observation.Cursor, Data: observation.Data,
		}, interval, time.Now())
		if err != nil {
			return nil, err
		}
		switch watch.TerminalState {
		case "succeeded":
			return facts, nil
		case "timed_out":
			return nil, fmt.Errorf("watch deadline elapsed before its condition was met")
		}
	}
}

func (r *workflowRunner) questionActive(ctx context.Context, q builtin.Question) bool {
	node, err := r.state.Node(ctx, q.NodeID)
	return err == nil && node.Status == store.WaitingForInput && node.AttemptID != nil && *node.AttemptID == q.AttemptID &&
		node.Wait != nil && node.Wait.Ref == q.ID
}

func (r *workflowRunner) executeNotify(ctx context.Context, run store.WorkflowRun, step store.WorkflowStep, attempt store.Attempt,
	with map[string]any) (map[string]any, error) {
	title, _ := with["title"].(string)
	body, _ := with["body"].(string)
	prompt, _ := with["question"].(string)
	approval, _ := with["approval"].(bool)
	if !approval && prompt == "" {
		n, err := r.notifications.Notify(run.RunID, step.NodeID, attempt.ID, title, body, "")
		return map[string]any{"notification_id": n.ID}, err
	}
	auto, _ := with["auto_approve"].(bool)
	if approval && auto {
		return map[string]any{"approved": true, "answer": "approve"}, nil
	}
	if prompt == "" {
		prompt = strings.TrimSpace(title + "\n\n" + body)
	}
	if prompt == "" {
		return nil, fmt.Errorf("approval requires a question, title, or body")
	}
	var options []string
	if approval {
		options = []string{"approve", "reject"}
	} else if values, ok := with["options"].([]any); ok {
		for _, value := range values {
			option, ok := value.(string)
			if !ok || strings.TrimSpace(option) == "" {
				return nil, fmt.Errorf("question options must be nonempty strings")
			}
			options = append(options, strings.TrimSpace(option))
		}
	}
	if raw := with["timeout"]; raw != nil {
		timeout, err := positiveDuration(raw, "question timeout")
		if err != nil {
			return nil, err
		}
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, timeout)
		defer stop()
	}
	q, notice, err := r.notifications.AskAndNotify(run.RunID, step.NodeID, attempt.ID, prompt, options)
	if err != nil {
		return nil, err
	}
	if err := r.state.Transition(ctx, step.NodeID, store.WaitingForInput, &store.WaitReason{Kind: "human", Ref: q.ID}); err != nil {
		return nil, err
	}
	for {
		current, err := r.notifications.Question(q.ID)
		if err != nil {
			return nil, err
		}
		if current.Answered {
			out := map[string]any{"answer": current.Answer, "notification_id": notice.ID}
			if approval {
				out["approved"] = current.Answer == "approve"
				if current.Answer == "reject" {
					return out, fmt.Errorf("workflow approval was rejected")
				}
			}
			return out, nil
		}
		if err := waitForWorkflowTime(ctx, 150*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
