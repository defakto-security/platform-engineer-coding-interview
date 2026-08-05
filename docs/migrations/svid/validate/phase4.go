package main

import (
	"fmt"
	"os"
	"strings"
)

// RunPhase4 confirms decommission actually removed what it was supposed to
// remove, and - just as importantly - that removing the old path didn't
// change anything about the new path's trust boundary. Re-running the
// phase-1 negative test here is deliberate: decommissioning is exactly the
// kind of change that could accidentally coincide with someone loosening
// the claim validation rules "since we don't need the old path anymore."
func RunPhase4(cfg Config) bool {
	fatalIfMissingTools("kubectl", "gh")

	checks := []Check{
		{
			Name: "legacy ServiceAccount is gone",
			Run: func() error {
				return assertGone("serviceaccount", legacyServiceAccountName, "-n", cfg.K8sNamespace)
			},
		},
		{
			Name: "legacy ClusterRoleBinding is gone",
			Run:  func() error { return assertGone("clusterrolebinding", legacyClusterRoleBindingID) },
		},
		{
			Name: "legacy GitHub Actions secret is gone",
			Run: func() error {
				out, err := sh("gh", "secret", "list", "--repo", fmt.Sprintf("%s/%s", cfg.GithubOrg, cfg.GithubRepo))
				if err != nil {
					return fmt.Errorf("could not list secrets: %s", out)
				}
				if strings.Contains(out, cfg.LegacySecretName) {
					return fmt.Errorf("secret %s still present in repo secrets", cfg.LegacySecretName)
				}
				return nil
			},
		},
		{
			Name: "regression: out-of-scope SVID is still rejected after decommission",
			Run: func() error {
				token := os.Getenv("SVID_TEST_TOKEN_OUT_OF_SCOPE")
				if token == "" {
					return fmt.Errorf("set SVID_TEST_TOKEN_OUT_OF_SCOPE to re-run the phase-1 negative test")
				}
				return tokenReview(token, false, "")
			},
		},
		{
			Name: "regression: in-scope SVID still authenticates correctly after decommission",
			Run: func() error {
				token := os.Getenv("SVID_TEST_TOKEN_VALID")
				if token == "" {
					return fmt.Errorf("set SVID_TEST_TOKEN_VALID to re-run the phase-1 positive test")
				}
				return tokenReview(token, true, mappedUsername(cfg))
			},
		},
	}

	return RunChecks(checks)
}
