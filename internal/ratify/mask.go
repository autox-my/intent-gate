package ratify

// Scanner masks the secrets in a root intent and reports how many it replaced (stub).
type Scanner func(text string) (masked string, redactions int, err error)

// Mask replaces secrets in text with [REDACTED:<rule-id>] (stub).
func Mask(text string) (string, int, error) { return text, 0, nil }
