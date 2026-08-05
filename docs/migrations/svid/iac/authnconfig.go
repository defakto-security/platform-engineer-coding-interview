package main

import "fmt"

// authnConfigTemplate is Kubernetes' Structured Authentication Configuration
// (apiserver.config.k8s.io/v1beta1) for trusting Defakto as an additional
// issuer, scoped to exactly one repo/workflow/environment. This is the one
// piece of the design that is genuinely cloud-agnostic: every managed
// offering that supports additional JWT issuers converges on this file, even
// though *delivering* it to the control plane is CSP-specific (see
// declareAuthnConfigPush in main.go).
//
// Kept as literal YAML rather than marshalled Go structs on purpose: this
// document is the artifact a reviewer needs to read and compare against the
// upstream API reference, and there is no dynamic structure here - only six
// interpolated strings. Field names are approximate to the upstream API as of
// writing; verify against the target cluster's Kubernetes version before
// treating this as copy-paste-able.
const authnConfigTemplate = `apiVersion: apiserver.config.k8s.io/v1beta1
kind: AuthenticationConfiguration
jwt:
  - issuer:
      url: %[1]s
      audiences: [%[2]s]
      audienceMatchPolicy: MatchAny
    claimMappings:
      # Defakto's SPIFFE ID becomes the Kubernetes username, namespaced with a
      # prefix so it can never collide with an existing
      # "system:serviceaccount:..." subject.
      username:
        claim: sub
        prefix: "defakto:"
    # Redundant with the RBAC scoping in main.go on purpose - see README
    # "Narrow trust is enforced twice." These rules are what stop "trust
    # Defakto" from becoming "trust every SPIFFE ID Defakto could ever issue."
    claimValidationRules:
      - claim: repository
        requiredValue: %[3]s/%[4]s
      - claim: workflow
        requiredValue: %[5]s
      - claim: environment
        requiredValue: %[6]s
`

func buildAuthenticationConfiguration(issuerURL, audience, org, repo, workflowName, environment string) string {
	return fmt.Sprintf(authnConfigTemplate, issuerURL, audience, org, repo, workflowName, environment)
}
