// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: snapdiff capture <report.json> <snapshot.json>\n")
		fmt.Fprintf(os.Stderr, "       snapdiff compare <report.json> <snapshot.json>\n")
		os.Exit(2)
	}

	cmd := args[0]
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: snapdiff %s <report.json> <snapshot.json>\n", cmd)
		os.Exit(2)
	}

	reportPath := args[1]
	snapshotPath := args[2]

	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading report: %v\n", err)
		os.Exit(2)
	}

	switch cmd {
	case "capture":
		runCapture(reportData, snapshotPath)
	case "compare":
		snapshotData, err := os.ReadFile(snapshotPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading snapshot: %v\n", err)
			os.Exit(2)
		}
		runCompare(reportData, snapshotData)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(2)
	}
}

func runCapture(reportData []byte, snapshotPath string) {
	snap, err := captureSnapshot(reportData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	// Count total results for summary
	totalResults := 0
	for _, cs := range snap.Cases {
		for _, ps := range cs.Phases {
			totalResults += len(ps.Results)
		}
	}

	// Write snapshot
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling snapshot: %v\n", err)
		os.Exit(2)
	}

	if err := os.WriteFile(snapshotPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing snapshot: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("%d results across %d cases\n", totalResults, len(snap.Cases))
	os.Exit(0)
}

func runCompare(reportData []byte, snapshotData []byte) {
	diffs, err := compareSnapshots(reportData, snapshotData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	// Parse snapshot to count entries
	var snap Snapshot
	if err := json.Unmarshal(snapshotData, &snap); err != nil {
		fmt.Fprintf(os.Stderr, "error reading snapshot: %v\n", err)
		os.Exit(2)
	}

	entryCount := 0
	for _, cs := range snap.Cases {
		for _, ps := range cs.Phases {
			entryCount += len(ps.Results)
		}
	}

	// Print summary
	fmt.Println(summarizeDiff(diffs, entryCount))

	if len(diffs) > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}
