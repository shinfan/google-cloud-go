// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build go1.15

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type indexer interface {
	get(prefix string, since time.Time) (entries []indexEntry, err error)
}

// indexClient is used to access index.golang.org.
type indexClient struct{}

var _ indexer = indexClient{}

// indexEntry represents a line in the output of index.golang.org/index.
type indexEntry struct {
	Path      string
	Version   string
	Timestamp time.Time
}

// newModules returns the new modules with the given prefix.
//
// newModules uses index.golang.org/index?since=timestamp to find new module
// versions since the given timestamp.
//
// newModules stores the timestamp of the last successful run in Datastore. If
// there is no value in Datastore, newModules defaults to 10 days ago.
func newModules(ctx context.Context, i indexer, tSaver timeSaver, prefix string) ([]indexEntry, error) {
	since, err := tSaver.get(ctx)
	if err != nil {
		return nil, err
	}
	fiveMinAgo := time.Now().Add(-5 * time.Minute).UTC() // When to stop processing.
	// Use a map to prevent duplicates.
	entries := map[indexEntry]struct{}{}
	log.Printf("Fetching index.golang.org entries since %s", since.Format(time.RFC3339))
	count := 0
	for {
		count++
		cur, err := i.get(prefix, since)
		if err != nil {
			return nil, err
		}
		if len(cur) == 0 {
			return nil, fmt.Errorf("Found 0 entries in index response")
		}
		since = cur[len(cur)-1].Timestamp
		for _, e := range cur {
			entries[e] = struct{}{}
		}
		if since.After(fiveMinAgo) {
			break
		}
	}
	log.Printf("Parsed %d index.golang.org pages up to %s", count, since.Format(time.RFC3339))
	if err := tSaver.put(ctx, since); err != nil {
		return nil, err
	}

	result := []indexEntry{}
	for e := range entries {
		result = append(result, e)
	}
	return result, nil
}

// get fetches a single page of modules from index.golang.org/index.
//
// last is the time of the latest module in the list.
func (indexClient) get(prefix string, since time.Time) ([]indexEntry, error) {
	entries := []indexEntry{}
	sinceString := since.Format(time.RFC3339)
	resp, err := http.Get("https://index.golang.org/index?since=" + sinceString)
	if err != nil {
		return nil, err
	}

	s := bufio.NewScanner(resp.Body)
	for s.Scan() {
		e := indexEntry{}
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(e.Path, prefix) ||
			strings.Contains(e.Path, "internal") ||
			strings.Contains(e.Path, "third_party") ||
			strings.Contains(e.Version, "-") { // Filter out pseudo-versions.
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}
