// Package main is the phase-gated Pulumi program for the static-kubeconfig ->
// JWT-SVID migration described in ../objective.md. Converge to a given phase
// with:
//
//	pulumi config set phase <0..4>
//	pulumi preview
//
// See ../README.md for the phase narrative, the load-bearing assumptions,
// and why Defakto's control-plane config and the AuthenticationConfiguration
// push are modeled via pulumi-command rather than a native provider.
package main

import (
	"fmt"
	"strconv"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const svidClusterRoleBindingID = "ci-deployer-svid"

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")

		// Unset means phase 0 (preflight, declares nothing); anything else
		// must be a phase this program actually models. Silently falling back
		// to 0 on a typo would hide the mistake, and an out-of-range value
		// would land in the phase>=4 decommission path.
		phase := 0
		if raw := cfg.Get("phase"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("phase %q is not an integer: set it to 0-4", raw)
			}
			phase = v
		}
		if phase < 0 || phase > 4 {
			return fmt.Errorf("phase %d out of range: must be 0-4", phase)
		}

		org := cfg.Get("githubOrg")
		repo := cfg.Get("githubRepo")
		workflow := cfg.Get("githubWorkflow")
		workflowName := cfg.Get("githubWorkflowName")
		environment := cfg.Get("githubEnvironment")
		namespace := cfg.Get("k8sNamespace")
		clusterRoleName := cfg.Get("existingClusterRoleName")
		trustDomain := cfg.Get("defaktoTrustDomain")
		legacySecretName := cfg.Get("legacyGithubSecretName")

		spiffeID := fmt.Sprintf("spiffe://%s/gha/%s/%s", trustDomain, repo, workflow)
		issuerURL := fmt.Sprintf("https://issuer.defakto.id/%s", trustDomain)
		k8sAudience := fmt.Sprintf("k8s.%s", trustDomain)
		mappedUsername := "defakto:" + spiffeID

		ctx.Export("spiffeId", pulumi.String(spiffeID))
		ctx.Export("mappedK8sUsername", pulumi.String(mappedUsername))

		// Phase 0 declares nothing - it is preflight only, see
		// validate/preflight.go.
		//
		// Phase >= 1: establish trust. Additive and inert - nothing consumes
		// this path yet, so it can be applied with zero risk to the existing
		// pipeline.
		if phase >= 1 {
			if err := declareDefaktoFederationSource(ctx, cfg, trustDomain, org, repo, workflowName, environment, spiffeID); err != nil {
				return err
			}

			if err := declareAuthnConfigPush(ctx, issuerURL, k8sAudience, org, repo, workflowName, environment); err != nil {
				return err
			}

			if err := declareSVIDClusterRoleBinding(ctx, mappedUsername, clusterRoleName); err != nil {
				return err
			}
		}

		// Legacy static-credential resources exist through phase 3
		// (annotated pending-decommission from phase 3 on) and are dropped
		// entirely at phase 4, letting Pulumi's own diff destroy them.
		if phase < 4 {
			if err := declareLegacyResources(ctx, phase, namespace, clusterRoleName); err != nil {
				return err
			}
		}

		// Phase 4: point of no return. Delete the legacy secret by name;
		// its value was never represented here.
		if phase >= 4 {
			if err := decommissionLegacySecretCommand(ctx, org, repo, legacySecretName); err != nil {
				return err
			}
		}

		return nil
	})
}

// declareDefaktoFederationSource registers GitHub Actions' OIDC issuer as a
// trusted upstream with Defakto, scoped to exactly one repo/workflow/
// environment, and defines the claims -> SPIFFE-ID mapping. workflowName is
// the workflow's `name:` (its display name), because that - not the file
// name - is what GitHub puts in the token's `workflow` claim. This is
// declarative SaaS configuration (akin to Terraform-managing an Auth0
// tenant), not workload infrastructure - the CI job's runtime SVID fetch
// never goes through Pulumi.
//
// Modeled with pulumi-command because Go's Pulumi SDK has no in-process
// dynamic-provider API (unlike Node/Python's `pulumi.dynamic.Resource`).
// `defakto-cli` below stands in for whatever Defakto's real control-plane
// API/CLI is - swap the Create/Delete commands for real API calls when
// adapting this for production.
func declareDefaktoFederationSource(ctx *pulumi.Context, cfg *config.Config, trustDomain, org, repo, workflowName, environment, spiffeID string) error {
	apiToken := cfg.RequireSecret("defaktoApiToken")

	_, err := local.NewCommand(ctx, "defakto-federation-source", &local.CommandArgs{
		Create: pulumi.Sprintf(
			`defakto-cli federation-source apply --trust-domain %s `+
				`--issuer https://token.actions.githubusercontent.com `+
				`--audience defakto-svid `+
				`--claim repository=%s/%s --claim workflow=%s --claim environment=%s `+
				`--map-to %s`,
			trustDomain, org, repo, workflowName, environment, spiffeID,
		),
		Delete: pulumi.Sprintf(`defakto-cli federation-source delete --trust-domain %s --spiffe-id %s`, trustDomain, spiffeID),
		Environment: pulumi.StringMap{
			"DEFAKTO_API_TOKEN": apiToken,
		},
	})
	if err != nil {
		return fmt.Errorf("defakto federation source: %w", err)
	}
	return nil
}

// declareAuthnConfigPush renders the AuthenticationConfiguration (see
// authnconfig.go) and pushes it to the control plane. The push mechanism
// itself is CSP-specific (EKS: associate an identity provider config via a
// cluster config update; GKE/AKS: analogous cluster update calls); this
// wraps that behind a single command so the program stays cloud-agnostic.
// In a real implementation, branch push.sh (or this Create command) on a
// `targetCsp` config value and call the matching SDK
// (aws.eks/google-native/azure-native) instead of shelling out.
func declareAuthnConfigPush(ctx *pulumi.Context, issuerURL, audience, org, repo, workflowName, environment string) error {
	// Stdin is an ordinary input: changing the rendered config re-runs the
	// command on its own, so no explicit Triggers entry is needed.
	_, err := local.NewCommand(ctx, "apply-authn-config", &local.CommandArgs{
		// stub: ./scripts/push-authn-config.sh would branch per-CSP.
		Create: pulumi.String(`./scripts/push-authn-config.sh apply`),
		Delete: pulumi.String(`./scripts/push-authn-config.sh remove-issuer`),
		Stdin:  pulumi.String(buildAuthenticationConfiguration(issuerURL, audience, org, repo, workflowName, environment)),
	})
	if err != nil {
		return fmt.Errorf("apply authn config: %w", err)
	}
	return nil
}

// declareSVIDClusterRoleBinding binds the narrowly-scoped, claim-mapped
// username to the *existing* ClusterRole - the migration changes how the
// pipeline authenticates, not what it's permitted to do. This binding is
// additive; the legacy binding is untouched until phase 4.
func declareSVIDClusterRoleBinding(ctx *pulumi.Context, mappedUsername, clusterRoleName string) error {
	_, err := rbacv1.NewClusterRoleBinding(ctx, svidClusterRoleBindingID, &rbacv1.ClusterRoleBindingArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String(svidClusterRoleBindingID),
		},
		RoleRef: &rbacv1.RoleRefArgs{
			ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
			Kind:     pulumi.String("ClusterRole"),
			Name:     pulumi.String(clusterRoleName),
		},
		Subjects: rbacv1.SubjectArray{
			&rbacv1.SubjectArgs{
				Kind: pulumi.String("User"),
				Name: pulumi.String(mappedUsername),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("svid cluster role binding: %w", err)
	}
	return nil
}
