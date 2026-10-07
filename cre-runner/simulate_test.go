package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

var testRequest = &evaluationRequest{Repository: "acme/pool", PRNumber: 1, CampaignID: "c", Event: "opened"}

func testConfig(t *testing.T) *workflowConfig {
	cfg, err := buildWorkflowConfig(campaignRow{ID: "c", RewardAsset: "USDC", MaxRewardPerPR: "500"}, "https://api.github.com", "")
	require.NoError(t, err)
	return cfg
}

func TestParseSimulateOutput(t *testing.T) {
	res, err := parseSimulateOutput(fixture(t, "success.txt"))
	require.NoError(t, err)
	require.Equal(t, &evaluationResponse{
		Score: 30, Eligible: false, Reward: "0",
		EvaluationHash: "0x4ea2c44568aa735bfbbcdd681e0d608ca289cc09eef856139c8ab875a7ed63f2",
		PolicyHash:     "0xbb5dc2d2ee4cfb3d3a20f48dc37c0a388e09e7e29576530d4c0e4f880e740ea1",
	}, res)

	// Colored output parses the same.
	_, err = parseSimulateOutput(strings.ReplaceAll(fixture(t, "success.txt"), "✓", "\x1b[32m✓\x1b[0m"))
	require.NoError(t, err)

	_, err = parseSimulateOutput(fixture(t, "failure.txt"))
	var ee *evalError
	require.ErrorAs(t, err, &ee)
	require.Contains(t, err.Error(), "HTTP 401")

	_, err = parseSimulateOutput("something odd\n")
	require.ErrorAs(t, err, &ee)

	// The scorecard is kept verbatim for the dashboard.
	out := "Workflow Simulation Result:\n" + `"{\"score\":54,\"evaluation_hash\":\"0x1\",\"scorecard\":{\"gates\":[{\"gate\":\"min_score\"}]}}"` + "\n"
	res, err = parseSimulateOutput(out)
	require.NoError(t, err)
	require.JSONEq(t, `{"gates":[{"gate":"min_score"}]}`, string(res.Scorecard))
}

func TestChildEnvDropsRunnerSecrets(t *testing.T) {
	env := childEnv([]string{
		"PATH=/bin", "HOME=/home/app", "GOTOOLCHAIN=go1.25.3", "CRE_API_KEY=k", "OTHER_VALUE=o",
		"SUPABASE_SECRET_KEY=s", "GITHUB_WEBHOOK_SECRET=w", "LLM_API_KEY=l", "GITHUB_APP_PRIVATE_KEY_PATH=/k.pem",
		"GITHUB_APP_ID=1", "GITHUB_TOKEN_VALUE=stale", "REVIEWER_TOKEN_VALUE=stale", "REVIEWER_TOKEN=r",
		"CRE_LOGIN_YAML=c2Vzc2lvbg==", "CRE_CONTEXT_YAML=Y3R4",
	})
	require.ElementsMatch(t, []string{
		"PATH=/bin", "HOME=/home/app", "GOTOOLCHAIN=go1.25.3", "CRE_API_KEY=k", "OTHER_VALUE=o",
	}, env)

	require.Equal(t, []string{"HOME=/home/app"}, childEnv([]string{"HOME=/home/app", "CRE_API_KEY="}))
}

// fakeCLI writes a script standing in for the cre binary.
func fakeCLI(t *testing.T, output string) string {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	require.NoError(t, os.WriteFile(out, []byte(output), 0o600))
	script := filepath.Join(dir, "cre")
	body := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args"
env > "` + dir + `/env"
while [ $# -gt 0 ]; do [ "$1" = "--config" ] && cp "$2" "` + dir + `/config.json"; shift; done
cat "` + out + `"
`
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	return script
}

func TestCLISimulator(t *testing.T) {
	t.Setenv("SUPABASE_SECRET_KEY", "must-not-leak")
	bin := fakeCLI(t, fixture(t, "success.txt"))
	dir := filepath.Dir(bin)
	sim := &cliSimulator{
		Bin: bin, Dir: t.TempDir(), Workflow: "test-workflow", Target: "local-simulation",
		Wasm: "/app/build/workflow.wasm", Timeout: 10 * time.Second, Tokens: ghapp.Static("ghs_abc"),
		ReviewerToken: "rev-token",
	}

	res, err := sim.Evaluate(context.Background(), testRequest, testConfig(t))
	require.NoError(t, err)
	require.Equal(t, 30, res.Score)

	raw, _ := os.ReadFile(filepath.Join(dir, "args"))
	args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	require.Equal(t, []string{"workflow", "simulate", "test-workflow", "--target", "local-simulation", "--config"}, args[:6])
	require.True(t, strings.HasPrefix(filepath.Base(args[6]), "cre-config-"))
	require.NoFileExists(t, args[6]) // Removed after the run.
	require.Equal(t, []string{"--non-interactive", "--trigger-index", "0", "--http-payload",
		`{"repository":"acme/pool","pr_number":1,"campaign_id":"c","event":"opened"}`,
		"--wasm", "/app/build/workflow.wasm"}, args[7:])

	var cfg map[string]any
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &cfg))
	require.Equal(t, "c", cfg["campaign"].(map[string]any)["id"])

	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	require.Contains(t, string(env), "GITHUB_TOKEN_VALUE=ghs_abc")
	require.Contains(t, string(env), "REVIEWER_TOKEN_VALUE=rev-token")
	require.NotContains(t, string(env), "must-not-leak")
}

func TestCLISimulatorFailureAndMissingBinary(t *testing.T) {
	sim := &cliSimulator{Bin: fakeCLI(t, fixture(t, "failure.txt")), Dir: t.TempDir(), Workflow: "w",
		Target: "t", Timeout: 10 * time.Second, Tokens: ghapp.Static("x")}
	_, err := sim.Evaluate(context.Background(), testRequest, testConfig(t))
	var ee *evalError
	require.ErrorAs(t, err, &ee)

	sim.Bin = "/nonexistent/cre"
	_, err = sim.Evaluate(context.Background(), testRequest, testConfig(t))
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "no result"), err.Error())
	require.NotErrorAs(t, err, &ee)
}
