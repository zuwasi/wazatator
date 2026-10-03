package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandSkillLibrary(t *testing.T) {
	dir := t.TempDir()
	got, err := expandSkillLibrary(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got, err = expandSkillLibrary("~")
	require.NoError(t, err)
	assert.Equal(t, home, got)

	_, err = expandSkillLibrary(filepath.Join(dir, "missing"))
	assert.ErrorContains(t, err, "is not a directory")
}
