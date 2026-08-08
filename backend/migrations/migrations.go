// Package migrations embeds the SQL migration files so the server binary can
// bring a database up to date without the source tree being present.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Load returns every migration keyed by file name. Names sort in the order the
// migrations must be applied, which is what the numeric prefix is for.
func Load() (map[string]string, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out[e.Name()] = string(b)
	}
	return out, nil
}
