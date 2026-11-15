package format

import (
	"encoding/json"
	"fmt"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// JSON renders the complete result as indented JSON. The result must already
// be masked.
func JSON(r model.Result) (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode result as JSON: %w", err)
	}

	return string(data) + "\n", nil
}
