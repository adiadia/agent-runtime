// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.sql
var embeddedFiles embed.FS

type File struct {
	Name string
	SQL  string
}

func Ordered() ([]File, error) {
	entries, err := fs.ReadDir(embeddedFiles, ".")
	if err != nil {
		return nil, err
	}

	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		body, err := embeddedFiles.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}

		files = append(files, File{
			Name: entry.Name(),
			SQL:  string(body),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	return files, nil
}
