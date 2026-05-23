// Package songs provides a small embedded catalog of well-known tracks used to
// generate human-friendly session names (e.g. "bohemian-rhapsody"). It exposes
// a Track type with a Slug() helper and a Pick function that returns a random
// track whose slug is not already taken.
package songs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
)

// Track is a single entry in the songs catalog.
type Track struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
	ISRC   string `json:"isrc"`
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Slug returns a URL-friendly slug derived from t.Name. The rules mirror
// internal/agent.slugify: lowercase, collapse non-alphanumeric runs to "-",
// trim leading/trailing "-", truncate to 40 chars, and require the first
// character to be [a-z0-9]. Returns "" if no valid slug can be produced.
func (t Track) Slug() string {
	slug := nonAlnumRe.ReplaceAllString(strings.ToLower(t.Name), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		// Include the byte at index 40 in the search window so a slug whose
		// 41st byte is '-' is recognised as already ending on a boundary at
		// index 40, rather than being trimmed back to the previous '-'.
		window := slug[:41]
		if cut := strings.LastIndexByte(window, '-'); cut > 0 {
			slug = slug[:cut]
		} else {
			slug = strings.TrimRight(slug[:40], "-")
		}
	}
	if slug == "" {
		return ""
	}
	if slug[0] < 'a' || slug[0] > 'z' {
		if slug[0] < '0' || slug[0] > '9' {
			return ""
		}
	}
	return slug
}

//go:embed catalog.json
var catalogJSON []byte

var catalog []Track

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(fmt.Sprintf("songs: failed to parse embedded catalog.json: %v", err))
	}
	if len(catalog) == 0 {
		panic("songs: embedded catalog.json is empty")
	}
}

// Pick returns a random track from the catalog whose Slug() is not present in
// existing. It retries up to 100 times to avoid collisions; if all attempts
// collide it returns the last attempt anyway. Panics if the catalog is empty.
func Pick(existing []string) Track {
	if len(catalog) == 0 {
		panic("songs: catalog is empty")
	}

	existingSet := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		existingSet[e] = struct{}{}
	}

	var tr Track
	for i := 0; i < 100; i++ {
		tr = catalog[rand.IntN(len(catalog))]
		if _, found := existingSet[tr.Slug()]; !found {
			return tr
		}
	}
	return tr
}
