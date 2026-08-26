package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/raft"
)

type selectorKind int

const (
	selAll   selectorKind = iota // no selector: show all
	selIndex                     // single index
	selRange                     // index range start..end
	selTail                      // ~N last entries
	selDate                      // date range since..until
	selType                      // filter by log type
)

type selector struct {
	kind       selectorKind
	index      uint64       // selIndex
	start, end uint64       // selRange
	tail       uint64       // selTail
	since      time.Time    // selDate (zero = open-ended)
	until      time.Time    // selDate (zero = open-ended)
	logType    raft.LogType // selType
}

// parseSelectors splits s on commas and parses each part as a selector.
// Returns a slice of selectors. A single element without commas returns a one-element slice.
func parseSelectors(s string) ([]selector, error) {
	parts := strings.Split(s, ",")
	sels := make([]selector, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		sel, err := parseSelector(p)
		if err != nil {
			return nil, err
		}
		sels = append(sels, sel)
	}
	if len(sels) == 0 {
		return nil, fmt.Errorf("empty selector")
	}
	return sels, nil
}

func parseSelector(s string) (selector, error) {
	if len(s) > 1 && s[0] == '~' {
		var n uint64
		if _, err := fmt.Sscanf(s[1:], "%d", &n); err == nil {
			return selector{kind: selTail, tail: n}, nil
		}
		return selector{}, fmt.Errorf("invalid tail selector: %s", s)
	}
	if parts := strings.SplitN(s, "..", 2); len(parts) == 2 {
		if looksLikeDate(parts[0]) || looksLikeDate(parts[1]) {
			return parseDateRange(parts[0], parts[1])
		}
		if parts[0] == "" || parts[1] == "" {
			return selector{}, fmt.Errorf("open-ended ranges require date format (YYYY-MM-DD): %s", s)
		}
		var a, b uint64
		if _, err := fmt.Sscanf(parts[0], "%d", &a); err != nil {
			return selector{}, fmt.Errorf("invalid range start: %s", parts[0])
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &b); err != nil {
			return selector{}, fmt.Errorf("invalid range end: %s", parts[1])
		}
		return selector{kind: selRange, start: a, end: b}, nil
	}
	// Log type names.
	if lt, ok := parseLogType(s); ok {
		return selector{kind: selType, logType: lt}, nil
	}
	var idx uint64
	if _, err := fmt.Sscanf(s, "%d", &idx); err == nil {
		return selector{kind: selIndex, index: idx}, nil
	}
	return selector{}, fmt.Errorf("invalid selector: %s", s)
}

// parseLogType maps a log type name (case-insensitive) to a raft.LogType.
func parseLogType(s string) (raft.LogType, bool) {
	switch strings.ToLower(s) {
	case "logcommand":
		return raft.LogCommand, true
	case "lognoop":
		return raft.LogNoop, true
	case "logbarrier":
		return raft.LogBarrier, true
	case "logconfiguration":
		return raft.LogConfiguration, true
	default:
		return 0, false
	}
}

func looksLikeDate(s string) bool {
	if s == "" {
		return false
	}
	// Must start with a 4-digit year to be considered a date.
	if len(s) < 10 {
		return false
	}
	return s[4] == '-' && s[7] == '-'
}

func parseDateRange(a, b string) (selector, error) {
	var sel selector
	sel.kind = selDate
	if a != "" {
		t, err := parseTime(a)
		if err != nil {
			return selector{}, fmt.Errorf("invalid start date: %w", err)
		}
		sel.since = t
	}
	if b != "" {
		t, err := parseTime(b)
		if err != nil {
			return selector{}, fmt.Errorf("invalid end date: %w", err)
		}
		sel.until = t
	}
	return sel, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q (expected RFC3339 or YYYY-MM-DD)", s)
}
