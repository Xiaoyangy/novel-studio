package main

import "github.com/chenhongyang/novel-studio/internal/host"

// Observe only process birth metadata, never command lines or environments.
// A birth token distinguishes a surviving call owner from a reused PID.
func pipelineUsageProcessIdentity(pid int) (string, bool, error) {
	return host.UsageProcessIdentity(pid)
}

func recoverPipelineInterruptedUsageCalls(meter *host.DurableUsageMeter, identity func(int) (string, bool, error)) error {
	return meter.RecoverInterruptedCalls(identity)
}
