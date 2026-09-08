package main

import (
	"context"

	"github.com/chenhongyang/novel-studio/internal/host"
)

// Observe only process birth metadata, never command lines or environments.
// A birth token distinguishes a surviving call owner from a reused PID.
func pipelineUsageProcessIdentity(pid int) (string, bool, error) {
	return host.UsageProcessIdentity(pid)
}

func (a *pipelineProjectAllAccounting) startCall(id, agent string) error {
	if err := context.Cause(a.ctx); err != nil {
		return err
	}
	if agent == "project_all_planner" || agent == "project_all_world_simulator" {
		agent = "writer"
	}
	return a.meter.StartCall(id, agent, a.generationID, a.ownerPID, a.ownerStart)
}

func (a *pipelineProjectAllAccounting) recoverInterruptedCalls(identity func(int) (string, bool, error)) error {
	return recoverPipelineInterruptedUsageCalls(a.meter, identity)
}

func recoverPipelineInterruptedUsageCalls(meter *host.DurableUsageMeter, identity func(int) (string, bool, error)) error {
	return meter.RecoverInterruptedCalls(identity)
}
