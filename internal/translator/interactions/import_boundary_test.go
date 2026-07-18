package interactions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractionsTranslatorsStayOnV6ImportBoundary(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	root := filepath.Join(repoRoot, "internal", "translator")
	var violations []string
	errWalk := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || !strings.Contains(filepath.ToSlash(path), "/interactions/") {
			return nil
		}
		data, errRead := os.ReadFile(path)
		if errRead != nil {
			return errRead
		}
		forbidden := "github.com/router-for-me/CLIProxyAPI/" + "v7/"
		if strings.Contains(string(data), forbidden) {
			rel, errRel := filepath.Rel(repoRoot, path)
			if errRel != nil {
				rel = path
			}
			violations = append(violations, rel)
		}
		return nil
	})
	if errWalk != nil {
		t.Fatalf("scan Interactions translators: %v", errWalk)
	}
	if len(violations) > 0 {
		t.Fatalf("Interactions translators import v7 packages: %s", strings.Join(violations, ", "))
	}
}
