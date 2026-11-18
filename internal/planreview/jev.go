package planreview

import (
	"context"
	"net/http"
)

// Jev is TypeSafe's Jev behind Reviewer (stub: the red commit only compiles).
type Jev struct {
	BaseURL, Key, Model string
	HTTP                *http.Client
}

// Review is a stub.
func (Jev) Review(context.Context, string, string) ([]Finding, error) { return nil, nil }

// Items is a stub.
func Items(text string, max int) []string { return nil }
