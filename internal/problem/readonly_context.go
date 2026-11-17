package problem

import (
	"errors"
	"sort"
)

// Read-only context supplements generation, not the reusable bank identity.
// In particular, old candidates remain discoverable and require current normal
// self-tests/evaluation rather than inheriting proof from a driver observation.
func prepareReadOnlyContext(in []ReadOnlyFile, editable []File) ([]ReadOnlyFile, string, error) {
	if len(in) == 0 {
		return nil, "", nil
	}
	if len(in) > 8 {
		return nil, "", errors.New("too many read-only context files")
	}
	files := append([]ReadOnlyFile(nil), in...)
	seen := make(map[string]bool, len(editable)+len(files))
	for _, file := range editable {
		seen[file.Path] = true
	}
	total := 0
	for i := range files {
		file := &files[i]
		if !validPath(file.Path) || seen[file.Path] || file.Language == "" || file.Content == "" || len(file.Content) > 64<<10 {
			return nil, "", errors.New("invalid, duplicate, overlapping, or oversized read-only context file")
		}
		seen[file.Path] = true
		total += len(file.Content)
		if total > 128<<10 {
			return nil, "", errors.New("read-only context is too large")
		}
		for _, text := range []string{file.Path, file.Language, file.Content} {
			if err := ValidatePublicText(text); err != nil {
				return nil, "", err
			}
		}
		file.ContentHash = HashContent(file.Content)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, canonicalHash(files), nil
}
