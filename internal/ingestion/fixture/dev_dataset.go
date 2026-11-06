package fixture

import (
	"embed"
	"encoding/json"
	"fmt"
)

// devDatasetFS embeds the checked-in dev/test dataset at compile time
// (see testdata/generate.go for how it was produced) so loading it
// never depends on the process's current working directory the way a
// plain runtime file read would -- cmd/argus-worker (run from the
// repo root) and `go test` (which cds into this package's own
// directory) load the identical bytes either way.
//
//go:embed testdata/dev_dataset.json
var devDatasetFS embed.FS

// DevDataset decodes the embedded dev/test dataset. Data's own fields
// (Instruments, Candles, Quotes) carry no custom JSON tags, so the
// default encoding already matches what testdata/generate.go writes.
func DevDataset() (Data, error) {
	b, err := devDatasetFS.ReadFile("testdata/dev_dataset.json")
	if err != nil {
		return Data{}, fmt.Errorf("fixture: read embedded dev dataset: %w", err)
	}
	var data Data
	if err := json.Unmarshal(b, &data); err != nil {
		return Data{}, fmt.Errorf("fixture: decode embedded dev dataset: %w", err)
	}
	return data, nil
}
