//go:build unix

package main

import "syscall"

// auditOpenFlags keeps the audit log open from following a symlink or blocking on a FIFO.
const auditOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
