// Package arch holds architecture tests only: it has no production code.
//
// TestPurePackages reads the packages marked PURE in docs/design/README.md section 2
// and checks that none of them imports an I/O, network, randomness or adapter package,
// or reads the clock. The rule is what makes `intentgate verify` deterministic (REQ-0040).
package arch
