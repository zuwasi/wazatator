package orchestration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/microsoft/waza/internal/models"
)

// FilterTestCases returns the subset of testCases based on whether it matches tags or task display name, or task id glob patterns.
// - taskPatterns - matches either the task display name or the task ID.
// - tagPatterns - matches tags.
//
// If taskPatterns and tagPatterns are specified the result is the intersection of the matches between them.
// If both taskPatterns and tagPatterns are empty, all test cases are returned.
func FilterTestCases(testCases []*models.TestCase, taskPatterns []string, tagPatterns []string) ([]*models.TestCase, error) {
	if len(taskPatterns) == 0 && len(tagPatterns) == 0 {
		return testCases, nil
	}

	var matched []*models.TestCase

	for _, tc := range testCases {
		taskNameMatch, err := matchesTaskOrDisplayName(tc, taskPatterns)

		if err != nil {
			return nil, err
		}

		tagNameMatch, err := matchesTags(tc, tagPatterns)

		if err != nil {
			return nil, err
		}

		if taskNameMatch && tagNameMatch {
			matched = append(matched, tc)
		}
	}

	return matched, nil
}

// matchesTaskOrDisplayName reports whether a test case's DisplayName or TestID matches any pattern.
func matchesTaskOrDisplayName(tc *models.TestCase, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}

	for _, p := range patterns {
		nameMatch, err := filepath.Match(p, tc.DisplayName)

		if err != nil {
			return false, fmt.Errorf("invalid task filter pattern %q: %w", p, err)
		}

		if nameMatch {
			return true, nil
		}

		idMatch, err := filepath.Match(p, tc.TestID)

		if err != nil {
			return false, fmt.Errorf("invalid task filter pattern %q: %w", p, err)
		}

		if idMatch {
			return true, nil
		}
	}
	return false, nil
}

// A pattern starting with "!" excludes tasks with a matching tag, so
// --tags '!holdout' runs everything except held-out tasks.
func matchesTags(tc *models.TestCase, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}

	var include, exclude []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			exclude = append(exclude, strings.TrimPrefix(p, "!"))
		} else {
			include = append(include, p)
		}
	}

	anyTagMatches := func(pats []string) (bool, error) {
		for _, tag := range tc.Tags {
			for _, p := range pats {
				tagMatched, err := filepath.Match(p, tag)
				if err != nil {
					return false, fmt.Errorf("invalid tag filter pattern %q: %w", p, err)
				}
				if tagMatched {
					return true, nil
				}
			}
		}
		return false, nil
	}

	excluded, err := anyTagMatches(exclude)
	if err != nil || excluded {
		return false, err
	}
	if len(include) == 0 {
		return true, nil
	}
	return anyTagMatches(include)
}
