//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux)

package main

// isTerminal is false where there is no termios check: ratification then refuses, which is the
// safe answer.
func isTerminal(fd int) bool { return false }
