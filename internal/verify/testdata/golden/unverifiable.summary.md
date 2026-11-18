## The intent lock could not be verified

### Could not verify

- `.intent/01JD8Z6Q4M7R2T9X3B5N8K1C7G.yml`: `criteria: must have 1 to 8 criteria, has 0`

### Lock `.intent/01JD8Z6Q4M7R2T9X3B5N8K1C6F.yml` (revision 1)

- `C1`: `Findings are published as SARIF.`
  - footprint: `internal/report/**`, `!internal/report/legacy/**`
- `C2`: `Policy maps outcomes to conclusions.`
  - footprint: `internal/policy/**`

### Outside every footprint (1)

- `docs/readme.md` (modified): nearest criterion `C1` of lock `01JD8Z6Q4M7R2T9X3B5N8K1C6F`. If the change is intended, run `intentgate amend 01JD8Z6Q4M7R2T9X3B5N8K1C6F --criterion C1 --add 'docs/**'`
