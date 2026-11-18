package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"intentgate/internal/gitx"
	"intentgate/internal/lock"
)

// `intentgate stats` (audit-metrics.md §2, REQ-0041): the footprint-amendment rate and the other
// Phase 0 signals that git history alone can answer. The window ends at HEAD's committer time, so
// the same HEAD gives the same bytes; the command reads no clock.
//
// Exit codes: 0; 2 usage; 3 git cannot be read.

const (
	statsUsage   = "usage: intentgate stats [--since <N>d] [--format text|json]\n"
	statsSchema  = "intentgate.stats/1"
	notAvailable = "n/a"
)

// statsDoc is the intentgate.stats/1 document; the field order is the key order.
type statsDoc struct {
	Schema                 string        `json:"schema"`
	SinceDays              int           `json:"since_days"`
	WindowStart            string        `json:"window_start"`
	WindowEnd              string        `json:"window_end"`
	Head                   string        `json:"head"`
	Locks                  int           `json:"locks"`
	Invalid                int           `json:"invalid"`
	Amended                int           `json:"amended"`
	AmendmentRate          string        `json:"amendment_rate"`
	AmendmentRatio         string        `json:"amendment_ratio"`
	Methods                []methodCount `json:"methods"`
	MedianReviewDurationMS *int          `json:"median_review_duration_ms"`
}

type methodCount struct {
	Method string `json:"method"`
	Count  int    `json:"count"`
}

func runStats(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("stats", nil, stdout, deps)
	fs := newFlags("stats")
	since := fs.String("since", "30d", "")
	format := fs.String("format", "text", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(statsUsage, "")
	}
	days, ok := parseDays(*since)
	if !ok {
		return t.usage(statsUsage, fmt.Sprintf("--since must be a number of days such as 30d, got %q", *since))
	}
	if *format != "text" && *format != "json" {
		return t.usage(statsUsage, fmt.Sprintf("unknown --format %q", *format))
	}

	doc, err := computeStats(deps.Dir, days)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate stats: %v", err)
	}
	if *format == "json" {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return t.fail(exitUnverifiable, "intentgate stats: %v", err)
		}
		_, _ = stdout.Write(buf.Bytes())
		return exitOK
	}
	_, _ = io.WriteString(stdout, statsText(doc))
	return exitOK
}

// parseDays reads "<N>d" with N a positive integer.
func parseDays(s string) (int, bool) {
	num, ok := strings.CutSuffix(s, "d")
	if !ok || num == "" || strings.Trim(num, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || n > 36500 {
		return 0, false
	}
	return n, true
}

func computeStats(dir string, days int) (statsDoc, error) {
	headTime, err := gitx.CommitTime(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}
	head, err := gitx.Resolve(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}
	start := headTime.AddDate(0, 0, -days)
	adds, err := gitx.LockAdditions(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}

	doc := statsDoc{
		Schema: statsSchema, SinceDays: days, WindowStart: utcSeconds(start), WindowEnd: utcSeconds(headTime), Head: head,
		Methods: []methodCount{},
	}
	methods := map[string]int{}
	var durations []int
	for _, a := range adds {
		if a.Time.Before(start) || a.Time.After(headTime) {
			continue
		}
		data, found, err := gitx.ShowFile(dir, "HEAD", a.Path)
		if err != nil {
			return statsDoc{}, err
		}
		if !found {
			continue // removed since
		}
		l, err := lock.Parse(data)
		if err != nil || len(lock.Validate(l)) > 0 {
			doc.Invalid++
			continue
		}
		doc.Locks++
		for _, am := range l.Amendments {
			if am.Kind == lock.KindAddGlob {
				doc.Amended++
				break
			}
		}
		methods[l.Ratification.Method]++
		if ms := l.Ratification.ReviewDurationMS; ms != nil {
			durations = append(durations, *ms)
		}
	}

	doc.AmendmentRatio = fmt.Sprintf("%d/%d", doc.Amended, doc.Locks)
	doc.AmendmentRate = notAvailable
	if doc.Locks > 0 {
		doc.AmendmentRate = fmt.Sprintf("%.4f", float64(doc.Amended)/float64(doc.Locks))
	}
	for m, n := range methods {
		doc.Methods = append(doc.Methods, methodCount{m, n})
	}
	sort.Slice(doc.Methods, func(i, j int) bool { return doc.Methods[i].Method < doc.Methods[j].Method })
	if len(durations) > 0 {
		sort.Ints(durations)
		m := durations[(len(durations)-1)/2] // the lower middle for an even count
		doc.MedianReviewDurationMS = &m
	}
	return doc, nil
}

func statsText(d statsDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "window: %s to %s (%dd)\n", d.WindowStart, d.WindowEnd, d.SinceDays)
	fmt.Fprintf(&b, "locks: %d valid, %d invalid\n", d.Locks, d.Invalid)
	if d.AmendmentRate == notAvailable {
		fmt.Fprintf(&b, "footprint-amendment rate: %s (%s)\n", notAvailable, d.AmendmentRatio)
	} else {
		fmt.Fprintf(&b, "footprint-amendment rate: %s (%s)\n", d.AmendmentRate, d.AmendmentRatio)
	}
	parts := []string{}
	for _, m := range d.Methods {
		parts = append(parts, fmt.Sprintf("%s %d", m.Method, m.Count))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	fmt.Fprintf(&b, "ratification methods: %s\n", strings.Join(parts, ", "))
	if d.MedianReviewDurationMS == nil {
		fmt.Fprintf(&b, "median review duration: %s\n", notAvailable)
	} else {
		fmt.Fprintf(&b, "median review duration: %d ms\n", *d.MedianReviewDurationMS)
	}
	return b.String()
}
