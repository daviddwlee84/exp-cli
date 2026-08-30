package controller

import (
	"context"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/pueue"
)

// PueueScheduler adapts the narrow Pueue package to the controller interface.
// Environment is deliberately explicit because Pueue persists submitted task
// environments in daemon state.
type PueueScheduler struct {
	Adapter     pueue.Adapter
	Environment execx.Environment
}

func (scheduler PueueScheduler) Snapshot(ctx context.Context) (SchedulerSnapshot, error) {
	status, err := scheduler.Adapter.Status(ctx)
	if err != nil {
		return SchedulerSnapshot{}, err
	}
	result := SchedulerSnapshot{Tasks: make([]SchedulerTask, 0, len(status.Tasks))}
	for _, task := range status.Tasks {
		result.Tasks = append(result.Tasks, SchedulerTask{ID: task.ID, Label: task.Label, Group: task.Group, State: string(task.State)})
	}
	return result, nil
}

func (scheduler PueueScheduler) Submit(ctx context.Context, dispatch Dispatch) (int64, error) {
	allowed := execx.MinimalAllowlist()
	for _, variable := range scheduler.Environment.Variables() {
		if !variable.Sensitive {
			allowed = append(allowed, variable.Name)
		}
	}
	allowed = append(allowed, dispatch.AllowedEnv...)
	environment, err := execx.NewEnvironment(uniqueEnvironmentNames(allowed))
	if err != nil {
		return 0, err
	}
	return scheduler.Adapter.Submit(ctx, pueue.SubmitRequest{
		Group: dispatch.Group, Label: dispatch.Label, Priority: dispatch.Priority,
		WorkingDir: dispatch.WorkingDir, Worker: dispatch.Worker, WorkerArgs: dispatch.WorkerArgs,
		Environment: environment,
	})
}

func uniqueEnvironmentNames(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
