package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/waza/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendImpactLog(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\ndescription: d\n---\nbody\n"), 0o644))

	spec := &models.EvalSpec{SpecIdentity: models.SpecIdentity{Name: "demo-eval"}, SkillName: "my-skill",
		Config: models.Config{ModelID: "sonnet", SkillPaths: []string{skillDir}}}
	outcome := &models.EvaluationOutcome{
		Digest:         models.OutcomeDigest{TotalTests: 2, Succeeded: 1, SuccessRate: 0.5, AggregateScore: 0.75},
		TriggerMetrics: &models.TriggerMetrics{Precision: 1, Recall: 0.25, Collisions: 1},
		IsBaseline:     true,
		BaselineOutcome: &models.EvaluationOutcome{
			Digest: models.OutcomeDigest{SuccessRate: 0.25},
		},
		SkillImpactStats: &models.DeltaStats{Tasks: 2, Iterations: 1000, MeanDelta: 0.25, PValue: 0.4},
	}
	log := filepath.Join(dir, "skill-impact.md")

	require.NoError(t, appendImpactLog(log, spec, dir, outcome))
	require.NoError(t, appendImpactLog(log, spec, dir, outcome))

	data, err := os.ReadFile(log)
	require.NoError(t, err)
	text := string(data)
	assert.Equal(t, 1, strings.Count(text, "# Skill impact log"), "header written once")
	assert.Equal(t, 2, strings.Count(text, "· demo-eval · sonnet"), "one entry per run")
	assert.Contains(t, text, "`my-skill` (`SKILL.md`, sha256 `")
	assert.Contains(t, text, "Tasks: 1/2 passed (50.0%)")
	assert.Contains(t, text, "recall 25.0%, collisions 1")
	assert.Contains(t, text, "Skill impact: 50.0% with vs 25.0% without")
	assert.Contains(t, text, "Decision: _kept / reverted")
}

func TestPrintSkillTransferMatrix(t *testing.T) {
	mk := func(with, without, delta float64, significant bool) *models.EvaluationOutcome {
		return &models.EvaluationOutcome{
			Digest:           models.OutcomeDigest{SuccessRate: with},
			IsBaseline:       true,
			BaselineOutcome:  &models.EvaluationOutcome{Digest: models.OutcomeDigest{SuccessRate: without}},
			SkillImpactStats: &models.DeltaStats{MeanDelta: delta, PValue: 0.01, Significant: significant},
		}
	}
	out := captureStdout(t, func() {
		printSkillTransferMatrix([]modelResult{
			{modelID: "haiku", outcome: mk(0.9, 0.5, 0.4, true)},
			{modelID: "opus", outcome: mk(0.4, 0.8, -0.4, true)},
		})
	})
	assert.Contains(t, out, "SKILL TRANSFER MATRIX")
	assert.Contains(t, out, "helps *")
	assert.Contains(t, out, "HURTS (negative transfer) *")
	assert.Contains(t, out, "Negative transfer: the skill lowers the pass rate on opus")

	// With runs present, rates are per run (Delta matches the bootstrap), and a
	// non-significant gain is labelled as such.
	runs := func(status ...models.Status) []models.RunResult {
		var rr []models.RunResult
		for _, s := range status {
			rr = append(rr, models.RunResult{Status: s})
		}
		return rr
	}
	o := mk(0, 0, 0.25, false)
	o.TestOutcomes = []models.TestOutcome{{TestID: "t1", Runs: runs(models.StatusPassed, models.StatusFailed)}, {TestID: "t2", Runs: runs(models.StatusFailed, models.StatusFailed)}}
	o.BaselineOutcome.TestOutcomes = []models.TestOutcome{{TestID: "t1", Runs: runs(models.StatusFailed, models.StatusFailed)}, {TestID: "t2", Runs: runs(models.StatusFailed, models.StatusFailed)}}
	perRun := captureStdout(t, func() { printSkillTransferMatrix([]modelResult{{modelID: "haiku", outcome: o}}) })
	assert.Contains(t, perRun, "25.0%       0.0%        +25.0 pp")
	assert.Contains(t, perRun, "helps (not significant)")

	tip := captureStdout(t, func() {
		printSkillTransferMatrix([]modelResult{{modelID: "haiku", outcome: &models.EvaluationOutcome{}}})
	})
	assert.Contains(t, tip, "add --baseline")
}
