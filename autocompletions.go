package main

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/exp/slog"
)

const (
	autocompleteLimit           = 25
	defaultAutocompletionExpiry = 15 * time.Second
)

type AutoCompletions struct {
	mu     sync.Mutex
	ac     map[string]*haystack
	expiry time.Time

	subscriptions *Subscriptions
}

func (ac *AutoCompletions) CollectionNames(serverID, input string) ([]string, error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	now := time.Now()
	if now.UTC().After(ac.expiry.UTC()) {
		ac.ac = make(map[string]*haystack)
		ac.expiry = now.Add(defaultAutocompletionExpiry)
		slog.Info("Evicting autocompletions cache", slog.Time("autocomplete_cache_expires_at", ac.expiry))
	}

	lookup, ok := ac.ac[serverID]
	if !ok {
		collections, err := ac.subscriptions.GetCollectionNames(serverID)
		if err != nil {
			return nil, err
		}

		h := NewHaystack(collections)

		ac.ac[serverID] = h
		lookup = h
	}

	// Just match as much as possible if there's no input.
	if input == "" {
		input = "."
	} else {
		input = regexp.QuoteMeta(input)
	}
	input = strings.ToLower(input)

	re, err := regexp.Compile(input)
	if err != nil {
		return nil, err
	}

	suggestions := lookup.FindAll(re)
	if len(suggestions) > autocompleteLimit {
		suggestions = suggestions[:autocompleteLimit]
	}

	return suggestions, nil
}

type haystack struct {
	search []byte
	domain []byte
	spans  []span
}

func NewHaystack(s []string) *haystack {
	domain := strings.Join(s, "")
	h := &haystack{
		search: []byte(strings.ToLower(domain)),
		domain: []byte(domain),
	}

	next := 0
	for _, c := range s {
		s := span{
			start: next,
			end:   next + len(c),
		}
		h.spans = append(h.spans, s)
		next += len(c)
	}

	return h
}

func (h *haystack) FindAll(re *regexp.Regexp) []string {
	matches := re.FindAllIndex(h.search, -1)

	set := make(map[string]struct{})

	for _, m := range matches {
		start, end := m[0], m[1]

		for _, s := range h.spans {
			if !s.Overlaps(start, end) {
				continue
			}

			set[string(h.domain[s.start:s.end])] = struct{}{}
		}
	}

	var found []string
	for k := range set {
		found = append(found, k)
	}

	return found
}

type span struct {
	start, end int
}

func (s span) Overlaps(start, end int) bool {
	min := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}

	max := func(a, b int) int {
		if a > b {
			return a
		}
		return b
	}

	return max(s.start, start) < min(s.end, end)
}
