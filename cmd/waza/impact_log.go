package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/microsoft/waza/internal/execution"
	"github.com/microsoft/waza/internal/models"
	"github.com/microsoft/waza/internal/utils"
)

const impactLogHeader = `# Skill impact log

One entry per ` + "`wazatator run --impact-log`" + `. Record under each entry what changed in the
skill (diff summary) and whether the change was kept or reverted, so failed
ideas are not proposed again.
`

// appendImpactLog appends a Markdown entry describing this run to path: which
// version of the skill ran (SKILL.md hash), and its scores. This mirrors the
// WikiSkill paper's skill-impact.md, a persistent history of skill edits and
// their measured effect.
func appendImpactLog(path string, spec *models.EvalSpec, specDir string, outcome *models.EvaluationOutcome) error {
	var b strings.Builder
	if _, err := os.Stat(path); os.IsNotExist(err) {
		b.WriteString(impactLogHeader)
	}

	fmt.Fprintf(&b, "\n## %s · %s · %s\n\n", time.Now().Format("2006-01-02 15:04"), spec.Name, spec.Config.ModelID)

	cwd, _ := os.Getwd()
	skillDirs := append([]string{cwd}, utils.ResolvePaths(spec.Config.SkillPaths, specDir)...)
	if file := execution.FindSkillFile(skillDirs, spec.SkillName); file != "" {
		if data, err := os.ReadFile(file); err == nil {
			sum := sha256.Sum256(data)
			fmt.Fprintf(&b, "- Skill: `%s` (`%s`, sha256 `%s`, %d lines)\n", spec.SkillName, filepath.Base(file),
				hex.EncodeToString(sum[:])[:12], strings.Count(string(data), "\n")+1)
		}
	} else if spec.SkillName != "" {
		fmt.Fprintf(&b, "- Skill: `%s` (file not found)\n", spec.SkillName)
	}

	d := outcome.Digest
	fmt.Fprintf(&b, "- Tasks: %d/%d passed (%.1f%%), aggregate score %.2f\n", d.Succeeded, d.TotalTests, d.SuccessRate*100, d.AggregateScore)
	if m := outcome.TriggerMetrics; m != nil {
		fmt.Fprintf(&b, "- Triggers: precision %.1f%%, recall %.1f%%, collisions %d\n", m.Precision*100, m.Recall*100, m.Collisions)
	}
	if outcome.IsBaseline && outcome.BaselineOutcome != nil {
		fmt.Fprintf(&b, "- Skill impact: %.1f%% with vs %.1f%% without\n", d.SuccessRate*100, outcome.BaselineOutcome.Digest.SuccessRate*100)
		if s := outcome.SkillImpactStats; s != nil {
			fmt.Fprintf(&b, "- %s\n", s.String())
		}
	}
	if n := len(skillLibraries); n > 0 {
		fmt.Fprintf(&b, "- Competing skill libraries: %d (%s)\n", n, strings.Join(skillLibraries, ", "))
	}
	if u := d.Usage; u != nil {
		fmt.Fprintf(&b, "- Tokens: %d in / %d out", u.InputTokens, u.OutputTokens)
		if u.Provider == "anthropic" { // Claude Code reports cost in USD
			cost := 0.0
			for _, m := range u.ModelMetrics {
				cost += m.RequestCost
			}
			fmt.Fprintf(&b, ", cost $%.2f", cost)
		}
		b.WriteString("\n")
	}
	b.WriteString("- Change: _describe the SKILL.md edit_\n- Decision: _kept / reverted, and why_\n")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening impact log: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}
