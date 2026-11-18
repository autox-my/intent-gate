//go:build !unix

package main

// auditOpenFlags is empty where O_NOFOLLOW and O_NONBLOCK don't exist; the regular-file check in
// writeAudit still refuses a special file after opening.
const auditOpenFlags = 0
