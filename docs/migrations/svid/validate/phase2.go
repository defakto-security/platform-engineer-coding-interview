package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// legacyRunWindow is how far the legacy path's most recent run may sit from
// the shadow run and still be evidence about the same window. A green deploy
// from three weeks ago says nothing about whether the shadow run disturbed
// the legacy path.
const legacyRunWindow = 24 * time.Hour

// RunPhase2 confirms the SVID path works end-to-end from an actual GitHub
// Actions run (not just a local TokenReview simulation), while proving the
// unmodified deploy path is still healthy in the same window. A shadow run
// ID is required - this phase is about a specific real execution, not a
// standing state check.
func RunPhase2(cfg Config) bool {
	fatalIfMissingTools("gh")

	repo := fmt.Sprintf("%s/%s", cfg.GithubOrg, cfg.GithubRepo)

	// Captured by the first check and read by the second, which has to know
	// *when* the shadow ran to say anything meaningful about the legacy path.
	// Safe because RunChecks executes in order.
	var shadowCreatedAt time.Time

	checks := []Check{
		{
			Name: "shadow workflow run (SVID auth, read-only call) completed successfully",
			Run: func() error {
				runID := os.Getenv("SHADOW_RUN_ID")
				if runID == "" {
					return fmt.Errorf("set SHADOW_RUN_ID to the gh run id of the shadow job to check")
				}
				// gh's own --jq does the extraction; no client-side JSON
				// decoding needed for two scalar fields.
				out, err := sh("gh", "run", "view", runID, "--repo", repo,
					"--json", "conclusion,createdAt", "--jq", `[.conclusion, .createdAt] | @tsv`)
				if err != nil {
					return fmt.Errorf("could not view run %s: %s", runID, out)
				}
				conclusion, createdAt, ok := splitRunFields(out)
				if !ok {
					return fmt.Errorf("unexpected run view output for %s: %q", runID, out)
				}
				shadowCreatedAt, err = time.Parse(time.RFC3339, createdAt)
				if err != nil {
					return fmt.Errorf("could not parse createdAt %q for run %s: %w", createdAt, runID, err)
				}
				if conclusion != "success" {
					return fmt.Errorf("shadow run %s concluded %q, expected success", runID, conclusion)
				}
				return nil
			},
		},
		{
			Name: "real deploy workflow (legacy auth path) is unaffected - most recent run in the shadow window still green",
			Run: func() error {
				if shadowCreatedAt.IsZero() {
					return fmt.Errorf("no shadow run timestamp - the shadow run check above must pass before the legacy path can be compared against its window")
				}
				out, err := sh("gh", "run", "list", "--repo", repo,
					"--workflow", cfg.GithubWorkflow, "--limit", "1",
					"--json", "conclusion,createdAt", "--jq", `.[0] | [.conclusion, .createdAt] | @tsv`)
				if err != nil {
					return fmt.Errorf("could not list runs: %s", out)
				}
				if out == "" {
					return fmt.Errorf("no runs found for %s", cfg.GithubWorkflow)
				}
				conclusion, createdAt, ok := splitRunFields(out)
				if !ok {
					return fmt.Errorf("unexpected run list output for %s: %q", cfg.GithubWorkflow, out)
				}
				legacyCreatedAt, err := time.Parse(time.RFC3339, createdAt)
				if err != nil {
					return fmt.Errorf("could not parse createdAt %q: %w", createdAt, err)
				}
				if skew := legacyCreatedAt.Sub(shadowCreatedAt); skew < -legacyRunWindow || skew > legacyRunWindow {
					return fmt.Errorf("most recent %s run started %s from the shadow run, outside the +/-%s window - re-run the legacy deploy path so this check covers the shadow window",
						cfg.GithubWorkflow, skew.Round(time.Minute), legacyRunWindow)
				}
				if conclusion != "success" {
					return fmt.Errorf("most recent %s run concluded %q, expected success", cfg.GithubWorkflow, conclusion)
				}
				return nil
			},
		},
		// Not automated here: confirming the shadow run's API server call
		// actually authenticated as the mapped SPIFFE-derived username
		// (rather than, say, silently no-op'ing). That requires querying
		// the control plane's audit log for a request from that username
		// in the run's time window - the query mechanism is CSP-specific
		// (CloudWatch/Cloud Logging/Azure Monitor), so it's left as a
		// documented manual step rather than faked here.
	}

	return RunChecks(checks)
}

// splitRunFields splits gh's tab-separated `[.a, .b] | @tsv` output. A run
// with no conclusion yet (still in progress) yields an empty first field,
// which the caller reports as a non-success conclusion.
func splitRunFields(out string) (conclusion, createdAt string, ok bool) {
	parts := strings.Split(out, "\t")
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
