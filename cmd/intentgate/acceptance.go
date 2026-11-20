package main

import "io"

const acceptanceUsage = "usage: intentgate acceptance --out-dir <dir> --repo <owner/name> --base <sha> --head <sha>\n"

func runAcceptance(args []string, stdout io.Writer, deps Deps) int { return exitOK }
