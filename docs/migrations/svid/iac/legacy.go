package main

import (
	"fmt"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	legacyServiceAccountName   = "ci-deployer"
	legacyClusterRoleBindingID = "ci-deployer-legacy"
)

// declareLegacyResources represents the pre-existing static-credential setup.
// We assume these were brought under Pulumi management via `pulumi import`
// at phase 0 (they predate this stack - nothing here *creates* new legacy
// access). Modeling them explicitly is what makes their removal in phase 4
// a normal, reviewable Pulumi diff instead of an out-of-band manual step.
//
// From phase 3 onward we only patch an annotation (signal of intent, no
// permission change). At phase 4 the caller simply stops invoking this
// function - the resources drop out of desired state and Pulumi destroys
// them on the next `pulumi up`.
func declareLegacyResources(ctx *pulumi.Context, phase int, namespace, clusterRoleName string) error {
	annotations := pulumi.StringMap{}
	if phase >= 3 {
		// Phase 3: cutover has happened, deploys now use the SVID path.
		// Nothing consumes this ServiceAccount's token anymore, but we hold
		// off deleting it for one full deploy cycle so a revert of the
		// workflow YAML alone is enough to roll back - no Pulumi apply
		// needed to un-revert.
		annotations["migration.defakto.io/status"] = pulumi.String("pending-decommission")
	}

	sa, err := corev1.NewServiceAccount(ctx, legacyServiceAccountName, &corev1.ServiceAccountArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:        pulumi.String(legacyServiceAccountName),
			Namespace:   pulumi.String(namespace),
			Annotations: annotations,
		},
	})
	if err != nil {
		return fmt.Errorf("legacy service account: %w", err)
	}

	_, err = rbacv1.NewClusterRoleBinding(ctx, legacyClusterRoleBindingID, &rbacv1.ClusterRoleBindingArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:        pulumi.String(legacyClusterRoleBindingID),
			Annotations: annotations,
		},
		RoleRef: &rbacv1.RoleRefArgs{
			ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
			Kind:     pulumi.String("ClusterRole"),
			Name:     pulumi.String(clusterRoleName),
		},
		Subjects: rbacv1.SubjectArray{
			&rbacv1.SubjectArgs{
				// Referencing sa's output is itself the dependency edge - no
				// explicit DependsOn needed.
				Kind:      pulumi.String("ServiceAccount"),
				Name:      sa.Metadata.Name().Elem(),
				Namespace: pulumi.String(namespace),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("legacy cluster role binding: %w", err)
	}

	return nil
}

// decommissionLegacySecretCommand deletes the GitHub Actions secret by name
// only - its value was never, and must never be, represented in this
// program. This is a one-shot imperative step (not a declarative resource
// with a destroy lifecycle) because there is nothing to converge to: once
// deleted, there's no "desired state" left to track.
func decommissionLegacySecretCommand(ctx *pulumi.Context, org, repo, secretName string) error {
	_, err := local.NewCommand(ctx, "decommission-legacy-secret", &local.CommandArgs{
		// Idempotent: `gh secret delete` on an already-absent secret exits
		// non-zero, so guard with a list+grep. Real implementation would
		// use the GitHub REST API directly rather than shelling to `gh`.
		Create: pulumi.String(fmt.Sprintf(
			`gh secret list --repo %s/%s | grep -q '^%s' && gh secret delete %s --repo %s/%s || echo "%s already absent"`,
			org, repo, secretName, secretName, org, repo, secretName,
		)),
	})
	if err != nil {
		return fmt.Errorf("decommission legacy secret: %w", err)
	}
	return nil
}
