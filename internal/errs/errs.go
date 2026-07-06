// Package errs provides application-level error sentinels for novel-studio.
// Callers wrap errors with fmt.Errorf("...: %w", errs.ErrXxx) and use
// errors.Is to detect categories.
//
// Provider runtime errors (rate_limit / timeout / stream_idle / network / auth
// / context_overflow) live in agentcore — use agentcore.ClassifyProvider,
// agentcore.IsFailoverEligible, agentcore.FailoverReason, and
// agentcore.IsStreamIdleMessage directly.
package errs

import "errors"

var (
	ErrConfig           = errors.New("config error")
	ErrProvider         = errors.New("provider error") // provider initialization / wiring
	ErrStoreRead        = errors.New("store read error")
	ErrStoreWrite       = errors.New("store write error")
	ErrToolArgs         = errors.New("tool args invalid")
	ErrToolPrecondition = errors.New("tool precondition failed")
	ErrToolConflict     = errors.New("tool conflict")
	ErrPhaseTransition  = errors.New("invalid phase transition")
	ErrFlowTransition   = errors.New("invalid flow transition")
)
