package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// tokenReviewTimeout bounds the TokenReview call. This runs inline in the
// deploy job's pre-deploy gate, where an API server that never answers must
// fail the gate rather than hang the job until the runner's own timeout.
const tokenReviewTimeout = 30 * time.Second

func mappedUsername(cfg Config) string {
	spiffeID := fmt.Sprintf("spiffe://%s/gha/%s/%s", cfg.TrustDomain, cfg.GithubRepo, cfg.GithubWorkflow)
	return "defakto:" + spiffeID
}

// RunPhase1 confirms trust was established correctly and, critically, that
// it's narrow: an out-of-scope token must be rejected. The negative test
// matters as much as the positive one - a passing positive test alone would
// also pass if trust had accidentally been granted cluster-wide.
func RunPhase1(cfg Config) bool {
	fatalIfMissingTools("kubectl")

	checks := []Check{
		{
			Name: "new SVID ClusterRoleBinding exists and targets the existing ClusterRole",
			Run: func() error {
				out, err := sh("kubectl", "get", "clusterrolebinding", svidClusterRoleBindingName, "-o", "jsonpath={.roleRef.name}")
				if err != nil {
					return fmt.Errorf("clusterrolebinding %s not found: %s", svidClusterRoleBindingName, out)
				}
				if out != cfg.ClusterRoleName {
					return fmt.Errorf("expected roleRef %q, got %q", cfg.ClusterRoleName, out)
				}
				return nil
			},
		},
		{
			Name: "new SVID ClusterRoleBinding subject matches the expected mapped username",
			Run: func() error {
				out, err := sh("kubectl", "get", "clusterrolebinding", svidClusterRoleBindingName, "-o", "jsonpath={.subjects[0].name}")
				if err != nil {
					return fmt.Errorf("could not read subject: %s", out)
				}
				want := mappedUsername(cfg)
				if out != want {
					return fmt.Errorf("expected subject %q, got %q - claimMappings.username prefix/claim may not match RBAC", want, out)
				}
				return nil
			},
		},
		{
			Name: "legacy ClusterRoleBinding is unaffected (no regression)",
			Run: func() error {
				out, err := sh("kubectl", "get", "clusterrolebinding", legacyClusterRoleBindingID, "-o", "jsonpath={.roleRef.name}")
				if err != nil {
					return fmt.Errorf("legacy binding missing or errored: %s", out)
				}
				if out != cfg.ClusterRoleName {
					return fmt.Errorf("legacy binding roleRef changed unexpectedly: %q", out)
				}
				return nil
			},
		},
		{
			Name: "in-scope test SVID authenticates as the expected mapped username (positive test)",
			Run: func() error {
				token := os.Getenv("SVID_TEST_TOKEN_VALID")
				if token == "" {
					return fmt.Errorf("set SVID_TEST_TOKEN_VALID to a test SVID minted by Defakto for %s/%s:%s to run this check",
						cfg.GithubOrg, cfg.GithubRepo, cfg.GithubWorkflow)
				}
				return tokenReview(token, true, mappedUsername(cfg))
			},
		},
		{
			Name: "out-of-scope test SVID is rejected (negative test - proves trust didn't leak cluster-wide)",
			Run: func() error {
				token := os.Getenv("SVID_TEST_TOKEN_OUT_OF_SCOPE")
				if token == "" {
					return fmt.Errorf("set SVID_TEST_TOKEN_OUT_OF_SCOPE to a test SVID minted for a *different* repo/workflow to run this check")
				}
				return tokenReview(token, false, "")
			},
		},
	}

	return RunChecks(checks)
}

// tokenReview presents a token to the API server's TokenReview API - the
// only way to actually exercise the AuthenticationConfiguration wiring
// end-to-end, as opposed to just checking RBAC objects exist. `kubectl
// auth can-i --as=<user>` does NOT exercise this: it assumes the identity
// rather than authenticating a token, so it would pass even if the
// AuthenticationConfiguration were misconfigured or absent.
func tokenReview(token string, expectAuthenticated bool, expectedUsername string) error {
	review := map[string]any{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenReview",
		"spec":       map[string]any{"token": token},
	}
	body, err := json.Marshal(review)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), tokenReviewTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "create", "--raw", "/apis/authentication.k8s.io/v1/tokenreviews", "-f", "-")
	cmd.Stdin = strings.NewReader(string(body))
	raw, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(raw))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("tokenreview request did not complete within %s (treated as a failure - never fall back to the legacy credential): %s", tokenReviewTimeout, out)
		}
		return fmt.Errorf("tokenreview request failed: %s", out)
	}

	var result struct {
		Status struct {
			Authenticated bool `json:"authenticated"`
			User          struct {
				Username string `json:"username"`
			} `json:"user"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return fmt.Errorf("could not parse tokenreview response: %w (%s)", err, out)
	}

	if result.Status.Authenticated != expectAuthenticated {
		return fmt.Errorf("expected authenticated=%v, got %v", expectAuthenticated, result.Status.Authenticated)
	}
	if expectAuthenticated && result.Status.User.Username != expectedUsername {
		return fmt.Errorf("expected username %q, got %q", expectedUsername, result.Status.User.Username)
	}
	return nil
}
