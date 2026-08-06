// Command validate runs the pre/post checks for one phase of the
// static-kubeconfig -> JWT-SVID migration (../objective.md, ../README.md).
//
// Usage:
//
//	go run . preflight    # phase 0
//	go run . phase1        # post trust-establishment
//	go run . phase2        # shadow/dual-run
//	go run . phase3        # cutover: pre-deploy gate + post-deploy checks, aggregated (review use)
//	go run . phase3-gate   # cutover: pre-deploy gate ONLY, fail-closed (inline CI use - see workflow-examples/deploy-after.yml)
//	go run . phase4        # decommission
//
// This assumes kubectl and gh are already authenticated against the target
// cluster and repository - it does not manage credentials itself, and its
// checks shell out rather than link client-go, so it stays runnable without
// vendoring cluster-specific auth plugins for this exercise.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate <preflight|phase1|phase2|phase3|phase3-gate|phase4>")
		os.Exit(2)
	}

	cfg := defaultConfig()

	var ok bool
	switch os.Args[1] {
	case "preflight":
		ok = RunPreflight(cfg)
	case "phase1":
		ok = RunPhase1(cfg)
	case "phase2":
		ok = RunPhase2(cfg)
	case "phase3":
		ok = RunPhase3(cfg)
	case "phase3-gate":
		// Deliberately not RunChecks: this must stop on the first failure
		// and produce a nonzero exit with no aggregation, since it's meant
		// to gate a real `kubectl apply` step. See PreDeployGate in
		// phase3.go.
		if err := PreDeployGate(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "pre-deploy gate failed, aborting: %v\n", err)
			ok = false
		} else {
			fmt.Println("[ OK ] pre-deploy gate")
			ok = true
		}
	case "phase4":
		ok = RunPhase4(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown phase %q\n", os.Args[1])
		os.Exit(2)
	}

	if !ok {
		os.Exit(1)
	}
}
