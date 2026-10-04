package models

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
)

// BootstrapIterations is the number of resamples used for significance tests,
// following the paired-bootstrap protocol in the WikiSkill paper (1,000).
const BootstrapIterations = 1000

// PairedRuns holds the pass/fail results of one task under two conditions
// (A and B), such as without and with a skill, or old and new results.
type PairedRuns struct {
	TaskID string
	A      []bool
	B      []bool
}

// DeltaStats summarizes the difference in pass rate (B minus A) across tasks,
// with a 95% confidence interval and a two-sided p-value from a paired,
// hierarchical bootstrap: tasks are resampled, then each task's runs are
// resampled within each condition, so both task variety and run-to-run noise
// count.
type DeltaStats struct {
	Tasks       int     `json:"tasks"`
	Iterations  int     `json:"iterations"`
	MeanDelta   float64 `json:"mean_delta"`
	CI95Lo      float64 `json:"ci95_lo"`
	CI95Hi      float64 `json:"ci95_hi"`
	PValue      float64 `json:"p_value"`
	Significant bool    `json:"significant"`
}

// PairedBootstrap computes DeltaStats for tasks that have runs under both
// conditions. It returns nil when no task has runs in both. The seed makes the
// result reproducible.
func PairedBootstrap(tasks []PairedRuns, iterations int, seed uint64) *DeltaStats {
	var usable []PairedRuns
	for _, t := range tasks {
		if len(t.A) > 0 && len(t.B) > 0 {
			usable = append(usable, t)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	if iterations <= 0 {
		iterations = BootstrapIterations
	}

	observed := 0.0
	for _, t := range usable {
		observed += PassRate(t.B) - PassRate(t.A)
	}
	observed /= float64(len(usable))

	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	means := make([]float64, iterations)
	for i := range means {
		sum := 0.0
		for range usable {
			t := usable[rng.IntN(len(usable))]
			sum += resampledRate(rng, t.B) - resampledRate(rng, t.A)
		}
		means[i] = sum / float64(len(usable))
	}
	sort.Float64s(means)

	atOrBelow, atOrAbove := 0, 0
	for _, m := range means {
		if m <= 0 {
			atOrBelow++
		}
		if m >= 0 {
			atOrAbove++
		}
	}
	p := 2 * math.Min(float64(atOrBelow), float64(atOrAbove)) / float64(iterations)
	if p > 1 {
		p = 1
	}

	return &DeltaStats{
		Tasks:       len(usable),
		Iterations:  iterations,
		MeanDelta:   observed,
		CI95Lo:      percentile(means, 0.025),
		CI95Hi:      percentile(means, 0.975),
		PValue:      p,
		Significant: p < 0.05,
	}
}

// PassRate returns the fraction of runs that passed.
func PassRate(runs []bool) float64 {
	n := 0
	for _, r := range runs {
		if r {
			n++
		}
	}
	return float64(n) / float64(len(runs))
}

func resampledRate(rng *rand.Rand, runs []bool) float64 {
	n := 0
	for range runs {
		if runs[rng.IntN(len(runs))] {
			n++
		}
	}
	return float64(n) / float64(len(runs))
}

// percentile reads a value from sorted data using nearest-rank.
func percentile(sorted []float64, q float64) float64 {
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// PairedRunsFromOutcomes pairs tasks by TestID across two outcomes (A, B) and
// collects each run's pass/fail status.
func PairedRunsFromOutcomes(a, b *EvaluationOutcome) []PairedRuns {
	byID := map[string][]bool{}
	for _, to := range a.TestOutcomes {
		byID[to.TestID] = runPasses(to.Runs)
	}
	var out []PairedRuns
	for _, to := range b.TestOutcomes {
		if aRuns, ok := byID[to.TestID]; ok {
			out = append(out, PairedRuns{TaskID: to.TestID, A: aRuns, B: runPasses(to.Runs)})
		}
	}
	return out
}

func runPasses(runs []RunResult) []bool {
	out := make([]bool, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.Status == StatusPassed)
	}
	return out
}

// String renders the result as one line, for example "Significance: +50.0 pp,
// 95% CI [+0.0, +100.0], p = 0.50 (paired bootstrap, 2 tasks, 1000 resamples)
// -> not significant ...".
func (s *DeltaStats) String() string {
	verdict := "not significant: add tasks or trials before trusting the difference"
	if s.Significant {
		verdict = "significant (p < 0.05)"
	}
	return fmt.Sprintf("Significance: %+.1f pp, 95%% CI [%+.1f, %+.1f], p = %.2f (paired bootstrap, %d tasks, %d resamples) -> %s",
		s.MeanDelta*100, s.CI95Lo*100, s.CI95Hi*100, s.PValue, s.Tasks, s.Iterations, verdict)
}
