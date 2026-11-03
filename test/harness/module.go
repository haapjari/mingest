package harness

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// ModuleDir returns the directory of the Go module, where go.mod is.
func ModuleDir() (string, error) {
	out, err := exec.CommandContext(
		context.Background(), "go", "env", "GOMOD",
	).Output()
	if err != nil {
		return "", err
	}

	return filepath.Dir(strings.TrimSpace(string(out))), nil
}
