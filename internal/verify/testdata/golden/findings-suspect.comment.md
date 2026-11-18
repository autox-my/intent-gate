<!-- intentgate:pr-comment -->
### IntentGate: Changes outside the intent lock's footprints, or a suspect lock

Advisory only: this check never blocks a merge.

**Suspect locks** (their criteria are still checked, but trust them less)

- `.intent/01JD8Z6Q4M7R2T9X3B5N8K1C6F.yml` (revision 1): retargeted
  - retargeted: the lock was ratified for a different base branch than this pull request's

### Outside every footprint (4)

- `cmd/new/main.go` (added): nearest criterion C1 of lock 01JD8Z6Q4M7R2T9X3B5N8K1C6F. If the change is intended, run `intentgate amend 01JD8Z6Q4M7R2T9X3B5N8K1C6F --criterion C1 --add 'cmd/new/**'`
- `internal/gitx/diff.go` (modified): nearest criterion C1 of lock 01JD8Z6Q4M7R2T9X3B5N8K1C6F. If the change is intended, run `intentgate amend 01JD8Z6Q4M7R2T9X3B5N8K1C6F --criterion C1 --add 'internal/gitx/**'`
- `pkg/a.go` (renamed): nearest criterion C1 of lock 01JD8Z6Q4M7R2T9X3B5N8K1C6F. If the change is intended, run `intentgate amend 01JD8Z6Q4M7R2T9X3B5N8K1C6F --criterion C1 --add 'pkg/**'`
- `pkg/b.go` (renamed): nearest criterion C1 of lock 01JD8Z6Q4M7R2T9X3B5N8K1C6F. If the change is intended, run `intentgate amend 01JD8Z6Q4M7R2T9X3B5N8K1C6F --criterion C1 --add 'pkg/**'`
