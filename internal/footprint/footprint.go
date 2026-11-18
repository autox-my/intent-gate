// Package footprint is the pure footprint check: given changed files, criteria and implicit
// footprints it reports each changed file that lies outside all of them.
package footprint

// Status is how a file changed in the diff.
type Status string

const (
	Added    Status = "added"
	Modified Status = "modified"
	Deleted  Status = "deleted"
	Renamed  Status = "renamed"
)

// ChangedFile is one entry of the diff. OldPath is set only for renamed files.
type ChangedFile struct {
	Path, OldPath string
	Status        Status
}

// Criterion is one criterion of one lock.
type Criterion struct {
	LockID, ID string
	Footprint  []string
}

// Config is the parsed .intentgate.yml.
type Config struct {
	Add, Remove []string
	TestPairing bool
}

// Implicit is the set of implicit footprints in force.
type Implicit struct{}

// Finding is one changed path outside every footprint.
type Finding struct {
	Path             string `json:"path"`
	Status           Status `json:"status"`
	NearestCriterion string `json:"nearest_criterion"`
	LockID           string `json:"lock_id"`
	Fix              string `json:"fix"`
}

// ParseConfig parses .intentgate.yml. nil or empty data yields the defaults.
func ParseConfig(data []byte) (Config, error) { return Config{}, nil }

// NewImplicit builds the implicit footprints from the defaults, the config and .gitattributes.
func NewImplicit(cfg Config, gitattributes []byte) (Implicit, error) { return Implicit{}, nil }

// Check reports each changed file outside every criterion's footprint and every implicit footprint.
func Check(changed []ChangedFile, criteria []Criterion, implicit Implicit) []Finding { return nil }
