package main

import "fmt"

// RunPreflight (phase 0) confirms the assumptions this whole plan rests on,
// before anything is touched. If any of these are wrong, every later phase's
// blast-radius reasoning is wrong too.
func RunPreflight(cfg Config) bool {
	fatalIfMissingTools("kubectl", "gh")

	checks := []Check{
		{
			Name: "legacy ServiceAccount exists",
			Run: func() error {
				out, err := sh("kubectl", "get", "serviceaccount", legacyServiceAccountName,
					"-n", cfg.K8sNamespace, "-o", "name")
				if err != nil {
					return fmt.Errorf("expected pre-existing SA %s/%s, got: %s", cfg.K8sNamespace, legacyServiceAccountName, out)
				}
				return nil
			},
		},
		{
			Name: "legacy ClusterRoleBinding targets the legacy ServiceAccount and existing ClusterRole",
			Run: func() error {
				out, err := sh("kubectl", "get", "clusterrolebinding", legacyClusterRoleBindingID, "-o", "jsonpath={.roleRef.name}")
				if err != nil {
					return fmt.Errorf("clusterrolebinding %s not found: %s", legacyClusterRoleBindingID, out)
				}
				if out != cfg.ClusterRoleName {
					return fmt.Errorf("expected roleRef %q, got %q - the plan assumes the role name is unchanged", cfg.ClusterRoleName, out)
				}
				return nil
			},
		},
		{
			// The OIDC ID token is free and always available to a workflow
			// that asks for it, so phase 0 asserts the *absence* of the
			// permission: it is added at cutover (phase 3), and finding it
			// already granted means the workflow is not in the pre-migration
			// state this plan's blast-radius reasoning assumes.
			Name: "GitHub Actions workflow does not request id-token: write yet (added at cutover)",
			Run: func() error {
				content, err := fetchWorkflowContent(cfg)
				if err != nil {
					return err
				}
				if requestsIDTokenWrite(content) {
					return fmt.Errorf("workflow %s already requests id-token: write - phase 0 expects the pre-migration workflow, so confirm nothing has already started the cutover", cfg.GithubWorkflow)
				}
				return nil
			},
		},
		// Not automated here: confirming no external OIDC/JWT issuer is
		// already trusted by the API server. Structured Authentication
		// Configuration is a control-plane startup input, not a live
		// Kubernetes API object - there is no `kubectl get` for it. In a
		// real preflight this would come from the CSP's own API (e.g. `aws
		// eks describe-cluster` and inspect the identity provider config,
		// or the cluster's applied AuthenticationConfiguration if the CSP
		// exposes one) or from the platform team's own change history.
		// Flagged rather than faked with a check that always passes.
	}

	fmt.Println("--- preflight: not automated, confirm manually ---")
	fmt.Println("  - no external OIDC/JWT issuer is currently trusted by the API server for this pipeline")
	fmt.Println("--- preflight: automated checks ---")

	return RunChecks(checks)
}
