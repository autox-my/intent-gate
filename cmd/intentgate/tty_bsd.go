//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package main

import "golang.org/x/sys/unix"

// isTerminal reports whether standard descriptor fd is a terminal: the termios ioctl succeeds.
// /dev/null is a character device but not a terminal, so it fails here.
func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}
