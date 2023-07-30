package main

import (
	"log"
	"sync"
	"time"
)

const defaultAutocompletionExpiry = 15 * time.Second

type autocompletionFingerprint string

type AutoCompletions struct {
	mu      sync.Mutex
	ac      map[autocompletionFingerprint][]string
	expires time.Time

	subscriptions *Subscriptions
}

func (ac *AutoCompletions) CollectionNames(serverID, input string) ([]string, error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	fingerprint := autocompletionFingerprint(serverID + input)

	now := time.Now()
	if now.UTC().After(ac.expires.UTC()) {
		log.Printf("[INFO] Autocompletions have expired")
		refreshed := make(map[autocompletionFingerprint][]string)
		ac.ac = refreshed
		ac.expires = now.Add(defaultAutocompletionExpiry)
	}

	completions, ok := ac.ac[fingerprint]
	if !ok {
		log.Printf("[INFO] Autocompletion cache miss")
		matches, err := ac.subscriptions.GetCollectionNameAutocompletionsForServer(serverID, input)
		if err != nil {
			return nil, err
		}

		ac.ac[fingerprint] = matches
		completions = matches
	}

	return completions, nil
}
