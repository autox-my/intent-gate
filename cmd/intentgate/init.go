package main

import "io"

// `intentgate init` (ratification.md §1.1 and §2): set a repository up for IntentGate.
//
// Exit codes: 0; 2 usage, or not in a git repository; 3 an existing file is unreadable or invalid.

const initUsage = "usage: intentgate init [--agent claude-code|generic] [--dry-run]\n"

func runInit(args []string, stdout io.Writer, deps Deps) int {
	return exitOK
}
