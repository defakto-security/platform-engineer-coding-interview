package main

import (
	"fmt"
	"os"
	"strings"
)

// RunPhase3 covers the cutover phase. It reports both the pre-deploy gate
// and the post-deploy checks together for design-review purposes, but note
// the distinction: PreDeployGate is meant to be called directly from inside
// the CI job (e.g. `go run . phase3-gate` from this directory) and must fail-closed on
// the first error - no aggregation, no "3 of 4 checks passed, proceeding
// anyway." This function's use of RunChecks (which aggregates) is only
// appropriate for a standalone review run, never for the inline gate.
func RunPhase3(cfg Config) bool {
	fatalIfMissingTools("kubectl", "gh")

	fmt.Println("--- pre-deploy gate (fail-closed; see PreDeployGate for the inline version) ---")
	preOK := true
	if err := PreDeployGate(cfg); err != nil {
		fmt.Printf("[FAIL] pre-deploy gate: %v\n", err)
		preOK = false
	} else {
		fmt.Println("[ OK ] pre-deploy gate")
	}

	fmt.Println("--- post-deploy checks ---")
	// Fetched once: both workflow assertions below read the same definition.
	workflow, workflowErr := fetchWorkflowContent(cfg)

	postChecks := []Check{
		{
			Name: "deploy workflow no longer references the legacy secret",
			Run: func() error {
				if workflowErr != nil {
					return workflowErr
				}
				if strings.Contains(workflow, cfg.LegacySecretName) {
					return fmt.Errorf("workflow still references secrets.%s - cutover is not complete", cfg.LegacySecretName)
				}
				return nil
			},
		},
		{
			Name: "workflow requests id-token: write (required for the OIDC->SVID exchange)",
			Run: func() error {
				if workflowErr != nil {
					return workflowErr
				}
				if !requestsIDTokenWrite(workflow) {
					return fmt.Errorf("workflow does not request id-token: write - the SVID exchange step will fail")
				}
				return nil
			},
		},
		{
			Name: "legacy resources are annotated pending-decommission, not yet removed",
			Run: func() error {
				out, err := sh("kubectl", "get", "serviceaccount", legacyServiceAccountName, "-n", cfg.K8sNamespace,
					"-o", "jsonpath={.metadata.annotations.migration\\.defakto\\.io/status}")
				if err != nil {
					return fmt.Errorf("could not read legacy service account: %s", out)
				}
				if out != "pending-decommission" {
					return fmt.Errorf("expected annotation pending-decommission, got %q - confirm the stack was applied at phase>=3", out)
				}
				return nil
			},
		},
	}
	postOK := RunChecks(postChecks)

	return preOK && postOK
}

// PreDeployGate is the fail-closed check meant to run as a step immediately
// before `kubectl apply` in the deploy job itself. If the SVID exchange or
// the resulting authentication fails, this returns an error and the caller
// must abort the deploy - never fall back to the static credential. That
// fallback is exactly the ambiguous, partially-applied failure mode the
// objective calls out as worse than a blocked deploy.
func PreDeployGate(cfg Config) error {
	token := os.Getenv("CI_SVID_TOKEN")
	if token == "" {
		return fmt.Errorf("CI_SVID_TOKEN is not set - the workflow's SVID-exchange step must run before this gate and must itself fail the job if the exchange fails")
	}
	return tokenReview(token, true, mappedUsername(cfg))
}
