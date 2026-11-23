package services

import (
	"context"
)

// ResumeState is what a resumed export needs from earlier runs of the same job. The job manager owns
// one per job and passes it with WithResumeState; ExecuteExport updates it after every completed batch.
// Obfuscation is deterministic, so a resumed export gives the same values without extra state.
type ResumeState struct {
	// MetricsCount is the number of lines in staging after the last completed batch.
	MetricsCount int
	// StagingBytes is the staging file size after the last completed batch.
	StagingBytes int64

	// recorded is true once a batch completed, so StagingBytes is valid even when it is 0.
	recorded bool
}

type resumeStateKey struct{}

// WithResumeState attaches a job's resume state to ctx.
func WithResumeState(ctx context.Context, state *ResumeState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, resumeStateKey{}, state)
}

// resumeStateFrom returns the job's resume state, or a private one when the caller has none.
// A fresh (non-resumed) run starts from an empty state.
func resumeStateFrom(ctx context.Context, resuming bool) *ResumeState {
	state, ok := ctx.Value(resumeStateKey{}).(*ResumeState)
	if !ok || state == nil {
		return &ResumeState{}
	}
	if !resuming {
		*state = ResumeState{}
	}
	return state
}

func (s *ResumeState) recordBatch(stagingBytes int64, metricsCount int) {
	s.MetricsCount = metricsCount
	s.StagingBytes = stagingBytes
	s.recorded = true
}
