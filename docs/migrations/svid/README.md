# Migration: CI/CD Kubernetes Auth — Static Kubeconfig → SPIFFE JWT-SVID

This directory is a **mock interview solution artifact** for the scenario in
[`objective.md`](./objective.md). It is a static IaC representation of a phased migration
plan, plus Go validation scripts, meant to be *presented and defended* in a design review —
not to be `pulumi up`'d against a real cluster.

## Layout

```text
docs/migrations/svid/
├── objective.md              # the scenario (given)
├── iac/                       # Pulumi (Go) program, phase-gated by stack config
│   ├── Pulumi.yaml
│   ├── main.go                 # phase gate + resource wiring
│   ├── authnconfig.go          # Kubernetes AuthenticationConfiguration (structured auth config)
│   └── legacy.go               # pre-existing static-credential resources (imported, then retired)
├── validate/                   # Go CLI: pre/post checks run outside Pulumi, per phase
│   ├── main.go
│   ├── preflight.go             # phase 0
│   ├── phase1.go                # post-trust-establishment
│   ├── phase2.go                # shadow/dual-run
│   ├── phase3.go                # cutover
│   └── phase4.go                # decommission
└── workflow-examples/          # annotated before/after GitHub Actions YAML (context, not applied)
    ├── deploy-before.yml
    └── deploy-after.yml
```

## Load-bearing assumptions

These are the assumptions that shape the design below. Everything not listed here is a
naming/example detail (repo, cluster, trust domain) — swap freely without changing the plan.

1. **Cloud-agnostic control plane.** We do not assume EKS/GKE/AKS specifically. The mechanism
   modeled is Kubernetes' own **Structured Authentication Configuration**
   (`apiserver.config.k8s.io/v1beta1 AuthenticationConfiguration`, beta since 1.30), which lets
   the API server trust multiple JWT issuers with per-issuer claim validation — this is the part
   that's actually portable across managed offerings. *Getting that file onto the control plane*
   is CSP-specific (EKS: `associate-identity-provider-config` / cluster config update; GKE:
   cluster update with the auth config field; AKS: equivalent). We abstract that last mile behind
   a single `local.Command` call and call out in comments exactly where a real implementation
   would branch by CSP.
2. **Defakto has no public Pulumi provider**, and — importantly — **Pulumi has no business
   touching the CI job's runtime SVID fetch at all**. The workload calls Defakto's
   `RemoteWorkloadAPI` (gRPC) at execution time to exchange the GitHub OIDC token for a
   JWT-SVID; that's a runtime/SDK concern living in the workflow YAML, not infrastructure. The
   only thing IaC legitimately owns on the Defakto side is **control-plane configuration**:
   registering GitHub Actions' OIDC issuer as a trusted upstream, scoped to a specific
   repo/workflow, and the claims→SPIFFE-ID mapping. That's declarative SaaS state, similar to
   managing an Auth0/Okta tenant via Terraform. We represent it with `pulumi-command`
   (`local.Command` wrapping a hypothetical `defaktoctl` CLI) rather than a hand-rolled dynamic
   provider, because Go's Pulumi SDK — unlike Node/Python — has no first-class in-process
   dynamic-provider API; `pulumi-command` is the idiomatic Go-SDK stand-in for "declaratively
   drive an API/CLI with no native provider." This is flagged in code, not silently papered over.
3. **RBAC subject, not new permissions.** The migration changes *how* the pipeline authenticates,
   not *what* it's allowed to do. The existing `ClusterRole` is referenced by name and left
   untouched; only the binding's subject changes (service account → mapped SPIFFE-derived
   username).
4. **Narrow trust is enforced twice, redundantly**: once in the `AuthenticationConfiguration`'s
   `claimValidationRules` (reject tokens whose `repository`/`workflow` claims don't match), and
   again in RBAC (the new binding only grants the mapped username from *that* claim set — no
   wildcard subjects). Belt-and-suspenders on purpose: trusting Defakto as an issuer must not
   become an implicit grant to every other SPIFFE ID Defakto could ever issue.
5. **Fail-closed over fail-open**, per the objective's stated priority (consistency over
   availability). The cutover step never falls back to the static credential if SVID exchange
   fails — a failed auth exchange fails the deploy job outright. This is enforced in the workflow
   (no `continue-on-error`, no fallback branch) and asserted by the phase-3 validation script.
6. **Legacy resources are assumed already-imported.** The static `ServiceAccount` and
   `ClusterRoleBinding` predate this stack. For this exercise we assume they were brought under
   management via `pulumi import` at phase 0 so their removal in phase 4 shows up as a normal
   Pulumi diff. The legacy GitHub Actions secret's *value* is never represented in code (secrets
   don't belong in IaC even conceptually) — it's referenced by name only, and phase 4 deletes it
   by name via a one-shot command, not by declaring/destroying a `github.ActionsSecret` resource.
7. **Exact field names/shape** of `AuthenticationConfiguration` and the `pulumi-command`
   resource args are approximate to the real APIs as of writing — verify against the target
   Kubernetes version's docs and the current `pulumi-command` provider before treating this as
   copy-paste-able.

## Phases

| Phase | Name | Pulumi diff | Workflow change | Reversible? |
|---|---|---|---|---|
| 0 | Preflight | none | none | n/a |
| 1 | Establish trust | + Defakto federation source, + `AuthenticationConfiguration` push, + new narrow `ClusterRoleBinding` | none (unused) | yes, trivially — nothing consumes the new path yet |
| 2 | Shadow validation | none | a subset of workflows fetch an SVID and make a **read-only** call over the new path, real deploys unchanged | yes |
| 3 | Cutover | legacy resources annotated `pending-decommission` | deploy workflows switch to SVID auth, fail-closed | yes — legacy path still intact, can revert workflow YAML |
| 4 | Decommission | legacy `ServiceAccount` + `ClusterRoleBinding` removed from program (destroyed); legacy GitHub secret deleted | none further | no — this is the point of no return; gate it on a full cycle of green phase-3 deploys |

Run any phase's plan with:

```sh
cd iac
pulumi config set phase 1   # 0..4
pulumi preview
```

## What I'd want to see from validation, per phase

See `validate/*.go` for the actual (runnable, `kubectl`/`gh`-shelling) checks. It's a separate Go
module, so run it from its own directory:

```sh
cd validate
go run . preflight   # preflight|phase1|phase2|phase3|phase3-gate|phase4
```

Summary of intent:

- **Phase 0 (preflight)**: confirm current state matches assumptions before touching anything —
  legacy SA/binding exist, no external OIDC issuer already trusted by the API server, and the
  deploy workflow doesn't request `id-token: write` yet (the permission is free — it's granted at
  cutover, so finding it already there means someone has started this migration elsewhere).
- **Phase 1 (post-trust)**: the API server accepts a *correctly scoped* test SVID and rejects an
  **out-of-scope** one (wrong repo/workflow claim) — the negative test matters as much as the
  positive one, since it's the proof that trust didn't leak cluster-wide. Also confirm the legacy
  path is completely unaffected (no regression).
- **Phase 2 (shadow)**: end-to-end token exchange + a real (read-only) API server call succeeds
  from an actual GitHub Actions run, not just a local simulation.
- **Phase 3 (cutover, pre-deploy gate)**: SVID exchange + auth succeeds *before* the deploy step
  runs — if it doesn't, the job must fail before attempting `kubectl apply`, never fall back.
  `validate phase3-gate` is the fail-closed, non-aggregating entry point meant to run inline in
  the workflow (see `workflow-examples/deploy-after.yml`); `validate phase3` aggregates both the
  gate and the post-deploy checks for a standalone design-review run. Post-deploy: confirm the
  deploy used the new path (absence of the legacy secret reference in the workflow definition).
- **Phase 4 (decommission)**: legacy `ServiceAccount`/`ClusterRoleBinding` return `NotFound`;
  legacy GitHub secret is gone from the repo; the phase-1 negative test (out-of-scope rejection)
  still passes — decommissioning shouldn't have widened trust as a side effect.
