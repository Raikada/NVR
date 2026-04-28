package yamlwrapper

import (
	"encoding/json"

	"github.com/goccy/go-yaml"
)

// Marshal serializes a value to YAML.
//
// We marshal through JSON first so the same json:"-" / json:"omitempty"
// discipline that drives Unmarshal also drives Marshal: any field tagged
// json:"-" stays out of mediamtx.yml on save, exactly as it stays out of
// the wire shape. This avoids divergence between what the recorder reads
// and what it writes back.
func Marshal(v any) ([]byte, error) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(jsonBytes)
}
