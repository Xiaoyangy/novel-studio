package main

// Pipeline unit/integration tests use temporary books. They must not start,
// replace or retarget the real dashboard listening on the user's port 8765.
// Explicit service tests can still exercise the launcher using isolated ports.
func init() {
	pipelineEnsureDashboard = func(string) {}
}
