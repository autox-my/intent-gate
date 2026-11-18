package ratify

import (
	"time"

	"intentgate/internal/lock"
)

// BuildInput is everything Build needs to make a lock (stub).
type BuildInput struct {
	ID               string
	BaseRef          string
	Source           string
	RootIntent       string
	Redactions       int
	Criteria         []lock.Criterion
	Ratifier         string
	RatifiedAt       time.Time
	Method           string
	ReviewDurationMS int
	DraftedBy        string
}

// Build makes a validated, hashed lock (stub).
func Build(in BuildInput) (lock.Lock, error) { return lock.Lock{}, nil }

// MaskAndBuild scans in.RootIntent, then builds the lock from the masked text (stub).
func MaskAndBuild(scan Scanner, in BuildInput) (lock.Lock, string, error) {
	return lock.Lock{}, "", nil
}
