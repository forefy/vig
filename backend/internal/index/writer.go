package index

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

// Rows per Parquet part: a part stays a few megabytes zstd-compressed, so
// the browser takes it by Range and the server stages it in one request.
// Rows per row group inside a part: DuckDB prunes groups by their column
// ranges, so a lookup by path or id reads one group, not the part.
const (
	rowsPerPart  = 200_000
	rowsPerGroup = 16_384
)

// writeTable writes rows as parts under <root>/index/<table>/target=<slug>/,
// the layout IndexParts walks and the browser mounts as a hive partition.
// A table with no rows writes nothing: the browser gives it an empty view.
func writeTable[T any](root, slug, table string, rows []T) ([]string, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	dir := filepath.Join(root, "index", table, "target="+slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var files []string
	for i := 0; i*rowsPerPart < len(rows); i++ {
		end := min((i+1)*rowsPerPart, len(rows))
		name := fmt.Sprintf("part-%04d.parquet", i+1)
		if err := writePart(filepath.Join(dir, name), rows[i*rowsPerPart:end]); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", table, name, err)
		}
		files = append(files, name)
	}
	return files, nil
}

// writePart writes one part, a row group every rowsPerGroup rows.
func writePart[T any](path string, rows []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := parquet.NewGenericWriter[T](f, parquet.Compression(&parquet.Zstd))
	for i := 0; i < len(rows); i += rowsPerGroup {
		if _, err := w.Write(rows[i:min(i+rowsPerGroup, len(rows))]); err != nil {
			f.Close()
			return err
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// clearTarget removes what an earlier build left for the slug, so a
// table the new build no longer writes does not linger.
func clearTarget(root, slug string) error {
	tables, err := os.ReadDir(filepath.Join(root, "index"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, t := range tables {
		if !t.IsDir() {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, "index", t.Name(), "target="+slug)); err != nil {
			return err
		}
	}
	return nil
}
