package output

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/natedelduca/go-rag-pack/internal/chunk"
)

// WriteJSONL writes a slice of chunks to a newline-delimited JSON file.
func WriteJSONL(path string, chunks []chunk.Chunk) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	writer := bufio.NewWriter(f)
	enc := json.NewEncoder(writer)

	for _, ch := range chunks {
		if err := enc.Encode(ch); err != nil {
			return err
		}
	}

	return writer.Flush()
}
