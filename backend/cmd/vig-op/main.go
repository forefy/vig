// vig-op is the operator CLI. It claims a queued audit, works it inside a
// throwaway container, and posts results through the job-scoped operator API.
//
// The CLI never holds a standing credential: the token it starts with can
// claim one job, and after the claim it is bound to that job until delivery.
// Everything it posts becomes a timeline event the customer can see, so there
// is no free-text channel to the user.
//
// Layout inside the sandbox (VIG_OP_HOME, default /work):
//
//	/work/job.json    the claimed job as the API returned it
//	/work/token       the bound operator token, mode 0600
//	/work/src         the checkout at head_sha
//	/work/findings.json  what `vig-op report` submits
package main

import (
	"fmt"
	"os"
	"strings"
)

const usage = `vig-op - operator CLI (runs only inside the audit sandbox)

  vig-op claim [--no-clone]          claim the next queued audit, save the job, clone at head_sha
  vig-op status                      print the claimed job and its timeline
                                     (finding prose: short paragraphs split by blank lines, "- " bullets,
                                      backticks for identifiers; no paragraph over 600 characters)
  vig-op verify [--so <id>=<path>]…  hash built programs, compare with the deployed bytecode, post matches
  vig-op event <state>               advance: cloning | programs_verified | analysis | human_review | delivered
  vig-op report [--check] <findings.json>
                                     validate against the findings schema, print advice, submit (--check: stop before)
  vig-op attach [--proof] <n>[.<step>] <file> "<caption>"
                                     upload a capture, recording (mp4, webm) or file for finding n, or for
                                     one of its flow steps, and write the reference into findings.json
  vig-op delay <duration> <reason>   push the ETA (e.g. 12h "queue over capacity")
  vig-op fail <reason>               fail the job; the customer's credit or slot is restored
  vig-op template                    print an empty findings.json for this job
  vig-op surface                     print the job's surface map with attention
  vig-op review [--note t] <node>…   mark nodes as looked at
  vig-op replay [--tag t] <sig>      re-run a transaction: outcome, units, accounts before and after, logs
  vig-op whatif [--set-account k=json] [--time 36h] [--arg name=value] <sig>
                                     replay with the world changed first (overrides need a surfnet on VIG_RPC)
  vig-op har [--host h] <file.har>   map routes and requests from a browser or proxy capture

Environment
  VIG_API        API base, default http://localhost:7112
  VIG_OP_TOKEN   an unbound operator token (only needed for claim)
  VIG_OP_HOME    working directory, default /work
  VIG_OP_CONTAINER=1 asserts the sandbox when auto-detection fails
  VIG_RPC        Solana RPC for replay, default the public mainnet endpoint
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if !inContainer() {
		fmt.Fprintln(os.Stderr, "vig-op: refusing to run outside a container. Customer code is only ever checked out inside the sandbox (deploy/operator).")
		os.Exit(3)
	}
	ws, err := openWorkspace()
	if err != nil {
		die(err)
	}
	var runErr error
	switch os.Args[1] {
	case "claim":
		runErr = cmdClaim(ws, os.Args[2:])
	case "status":
		runErr = cmdStatus(ws)
	case "surface":
		runErr = cmdSurface(ws)
	case "review":
		runErr = cmdReview(ws, os.Args[2:])
	case "replay":
		runErr = cmdReplay(ws, os.Args[2:])
	case "whatif":
		runErr = cmdWhatIf(ws, os.Args[2:])
	case "har":
		runErr = cmdHAR(ws, os.Args[2:])
	case "verify":
		runErr = cmdVerify(ws, os.Args[2:])
	case "event":
		runErr = cmdEvent(ws, os.Args[2:])
	case "report":
		runErr = cmdReport(ws, os.Args[2:])
	case "attach":
		runErr = cmdAttach(ws, os.Args[2:])
	case "delay":
		runErr = cmdDelay(ws, os.Args[2:])
	case "fail":
		runErr = cmdFail(ws, os.Args[2:])
	case "template":
		runErr = cmdTemplate(ws)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if runErr != nil {
		die(runErr)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "vig-op:", err)
	os.Exit(1)
}

// inContainer is the container guard. Docker leaves /.dockerenv, Kubernetes
// and containerd show up in the cgroup path, and the explicit variable covers
// sandboxes that hide both.
func inContainer() bool {
	if os.Getenv("VIG_OP_CONTAINER") == "1" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "lxc"} {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	if b, err := os.ReadFile("/proc/self/mountinfo"); err == nil && strings.Contains(string(b), "overlay") {
		return true
	}
	return false
}
