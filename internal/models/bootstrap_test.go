package models

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runs(pass, total int) []bool {
	out := make([]bool, total)
	for i := 0; i < pass; i++ {
		out[i] = true
	}
	return out
}

func TestPairedBootstrap_NoDifferenceIsNotSignificant(t *testing.T) {
	tasks := []PairedRuns{
		{TaskID: "a", A: runs(2, 3), B: runs(2, 3)},
		{TaskID: "b", A: runs(3, 3), B: runs(3, 3)},
	}
	s := PairedBootstrap(tasks, BootstrapIterations, 1)
	require.NotNil(t, s)
	assert.Equal(t, 0.0, s.MeanDelta)
	assert.False(t, s.Significant)
	assert.LessOrEqual(t, s.CI95Lo, 0.0)
	assert.GreaterOrEqual(t, s.CI95Hi, 0.0)
}

func TestPairedBootstrap_ConsistentGainIsSignificant(t *testing.T) {
	var tasks []PairedRuns
	for i := 0; i < 12; i++ {
		tasks = append(tasks, PairedRuns{TaskID: string(rune('a' + i)), A: runs(0, 3), B: runs(3, 3)})
	}
	s := PairedBootstrap(tasks, BootstrapIterations, 1)
	require.NotNil(t, s)
	assert.InDelta(t, 1.0, s.MeanDelta, 1e-9)
	assert.True(t, s.Significant)
	assert.Less(t, s.PValue, 0.05)
	assert.Greater(t, s.CI95Lo, 0.0)
}

// Two tasks with one improvement is the "single demo" case: a real-looking
// +50 pp that the test correctly refuses to call significant.
func TestPairedBootstrap_TooFewTasksIsNotSignificant(t *testing.T) {
	tasks := []PairedRuns{
		{TaskID: "a", A: runs(1, 1), B: runs(1, 1)},
		{TaskID: "b", A: runs(0, 1), B: runs(1, 1)},
	}
	s := PairedBootstrap(tasks, BootstrapIterations, 1)
	require.NotNil(t, s)
	assert.InDelta(t, 0.5, s.MeanDelta, 1e-9)
	assert.False(t, s.Significant)
	assert.Contains(t, s.String(), "not significant")
}

func TestPairedBootstrap_IsReproducibleAndSkipsUnpairedTasks(t *testing.T) {
	tasks := []PairedRuns{
		{TaskID: "a", A: runs(1, 3), B: runs(2, 3)},
		{TaskID: "b", A: runs(0, 3), B: runs(3, 3)},
		{TaskID: "only-a", A: runs(1, 1)},
	}
	s1 := PairedBootstrap(tasks, 500, 7)
	s2 := PairedBootstrap(tasks, 500, 7)
	assert.Equal(t, s1, s2)
	assert.Equal(t, 2, s1.Tasks)
	assert.Nil(t, PairedBootstrap([]PairedRuns{{TaskID: "x", A: runs(1, 1)}}, 100, 1))
}

func TestPairedRunsFromOutcomes(t *testing.T) {
	a := &EvaluationOutcome{TestOutcomes: []TestOutcome{
		{TestID: "t1", Runs: []RunResult{{Status: StatusFailed}, {Status: StatusPassed}}},
		{TestID: "t2", Runs: []RunResult{{Status: StatusPassed}}},
	}}
	b := &EvaluationOutcome{TestOutcomes: []TestOutcome{
		{TestID: "t1", Runs: []RunResult{{Status: StatusPassed}, {Status: StatusPassed}}},
		{TestID: "t3", Runs: []RunResult{{Status: StatusPassed}}},
	}}
	pairs := PairedRunsFromOutcomes(a, b)
	require.Len(t, pairs, 1)
	assert.Equal(t, "t1", pairs[0].TaskID)
	assert.Equal(t, []bool{false, true}, pairs[0].A)
	assert.Equal(t, []bool{true, true}, pairs[0].B)
	assert.True(t, strings.HasPrefix((&DeltaStats{Tasks: 1, Iterations: 1}).String(), "Significance:"))
}
