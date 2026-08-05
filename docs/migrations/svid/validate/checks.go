package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// These must match the resource names declared in ../iac/legacy.go and
// ../iac/main.go - duplicated here because this is a separate Go module
// with no dependency on the Pulumi program (and no reason to introduce one
// just to share three string constants).
const (
	legacyServiceAccountName   = "ci-deployer"
	legacyClusterRoleBindingID = "ci-deployer-legacy"
	svidClusterRoleBindingName = "ci-deployer-svid"
)

// Config holds the same identifiers as the Pulumi stack config
// (iac/Pulumi.yaml) - kept in sync by hand for this exercise. In a real
// implementation this would read the same values from `pulumi stack output`
// / `pulumi config` rather than duplicating them here.
type Config struct {
	GithubOrg         string
	GithubRepo        string
	GithubWorkflow    string
	GithubEnvironment string
	K8sNamespace      string
	ClusterRoleName   string
	TrustDomain       string
	LegacySecretName  string
}

func defaultConfig() Config {
	return Config{
		GithubOrg:         "acme-platform",
		GithubRepo:        "checkout-service",
		GithubWorkflow:    "deploy.yml",
		GithubEnvironment: "production",
		K8sNamespace:      "ci",
		ClusterRoleName:   "ci-deployer",
		TrustDomain:       "acme.defakto.id",
		LegacySecretName:  "KUBE_DEPLOY_KUBECONFIG",
	}
}

// Check is a single named assertion. Run reports failure via a non-nil
// error; the message should say what was expected vs. observed, since these
// results are meant to be read live during a deploy or a design review, not
// just fed to a test runner.
type Check struct {
	Name string
	Run  func() error
}

// RunChecks executes checks in order and reports all failures rather than
// stopping at the first one - useful for a design review walkthrough where
// you want the full picture, but note phase3.go's pre-deploy gate
// deliberately does NOT use this: it must stop hard on the first failure.
func RunChecks(checks []Check) bool {
	allPassed := true
	for _, c := range checks {
		if err := c.Run(); err != nil {
			fmt.Printf("[FAIL] %s: %v\n", c.Name, err)
			allPassed = false
			continue
		}
		fmt.Printf("[ OK ] %s\n", c.Name)
	}
	return allPassed
}

// sh runs a command and returns combined stdout+stderr, trimmed. It assumes
// `kubectl`/`gh` are already authenticated against the correct
// cluster/repo context - this script does not manage kubeconfig or gh auth
// itself.
func sh(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// assertGone asserts a cluster resource no longer exists, distinguishing
// "deleted" from "we couldn't tell" - a kubectl error that isn't NotFound
// (RBAC denial, unreachable API server) must not read as success.
func assertGone(kind, name string, extraArgs ...string) error {
	out, err := sh("kubectl", append([]string{"get", kind, name}, extraArgs...)...)
	if err == nil {
		return fmt.Errorf("%s %s still present: %s", kind, name, out)
	}
	if !strings.Contains(out, "NotFound") {
		return fmt.Errorf("unexpected error checking %s %s: %s", kind, name, out)
	}
	return nil
}

// fetchWorkflowContent reads the deploy workflow's YAML straight from GitHub
// (raw, not the base64 contents payload), so checks assert against what will
// actually run rather than whatever is checked out locally.
func fetchWorkflowContent(cfg Config) (string, error) {
	path := fmt.Sprintf(".github/workflows/%s", cfg.GithubWorkflow)
	out, err := sh("gh", "api", fmt.Sprintf("repos/%s/%s/contents/%s", cfg.GithubOrg, cfg.GithubRepo, path),
		"--jq", ".content", "-H", "Accept: application/vnd.github.raw+json")
	if err != nil {
		return "", fmt.Errorf("could not read workflow %s: %s", path, out)
	}
	return out, nil
}

// workflowDefinition is the sliver of a workflow this tool needs to reason
// about: `permissions` can appear at the top level, per-job, or both.
type workflowDefinition struct {
	Permissions permissionsSpec `yaml:"permissions"`
	Jobs        map[string]struct {
		Permissions permissionsSpec `yaml:"permissions"`
	} `yaml:"jobs"`
}

// permissionsSpec handles both shapes GitHub accepts: the `write-all` /
// `read-all` scalar shorthand, and the per-scope mapping.
type permissionsSpec struct {
	shorthand string
	scopes    map[string]string
}

func (p *permissionsSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		return value.Decode(&p.shorthand)
	case yaml.MappingNode:
		return value.Decode(&p.scopes)
	default:
		return fmt.Errorf("unexpected permissions node at line %d", value.Line)
	}
}

func (p permissionsSpec) grantsIDTokenWrite() bool {
	return p.shorthand == "write-all" || p.scopes["id-token"] == "write"
}

// requestsIDTokenWrite reports whether the workflow asks for the GitHub OIDC
// ID token. Without it there is nothing to exchange for an SVID. The YAML is
// parsed rather than string-matched on purpose: a commented-out
// `# id-token: write` (exactly what a half-finished cutover leaves behind)
// must not satisfy this check. Unparseable YAML reports false - an
// unreadable workflow is not evidence the permission is granted.
func requestsIDTokenWrite(content string) bool {
	var wf workflowDefinition
	if err := yaml.Unmarshal([]byte(content), &wf); err != nil {
		return false
	}
	if wf.Permissions.grantsIDTokenWrite() {
		return true
	}
	for _, job := range wf.Jobs {
		if job.Permissions.grantsIDTokenWrite() {
			return true
		}
	}
	return false
}

func fatalIfMissingTools(tools ...string) {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing required tools on PATH: %s\n", strings.Join(missing, ", "))
		os.Exit(2)
	}
}
