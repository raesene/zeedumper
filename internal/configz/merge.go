package configz

import (
	"encoding/json"
	"fmt"
)

// MergeResult contains the merged configz data and metadata about which fields
// were inserted from the defaults table.
type MergeResult struct {
	Merged map[string]interface{}
	Filled map[string]bool
}

// Merge parses rawJSON (the configz response body), fills in any fields missing
// due to omitempty with their known zero-value defaults for the given component
// and Kubernetes minor version, and returns the complete configuration.
//
// Fields already present in the configz response are never overwritten.
// For unknown components or versions, the parsed JSON is returned as-is with an
// empty Filled set.
func Merge(rawJSON string, component string, k8sMinor int) (*MergeResult, error) {
	var top map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &top); err != nil {
		return nil, fmt.Errorf("parsing configz JSON: %w", err)
	}

	cd := lookupDefaults(component, k8sMinor)
	if cd == nil {
		return &MergeResult{Merged: top, Filled: map[string]bool{}}, nil
	}

	inner := top
	if cd.wrapperKey != "" {
		wrapped, ok := top[cd.wrapperKey]
		if !ok {
			return &MergeResult{Merged: top, Filled: map[string]bool{}}, nil
		}

		inner, ok = wrapped.(map[string]interface{})
		if !ok {
			return &MergeResult{Merged: top, Filled: map[string]bool{}}, nil
		}
	}

	filled := make(map[string]bool)
	mergeDefaults(inner, cd.defaults, "", filled)

	return &MergeResult{Merged: top, Filled: filled}, nil
}

// mergeDefaults recursively inserts missing fields from defaults into config.
func mergeDefaults(config, defaults map[string]interface{}, prefix string, filled map[string]bool) {
	for k, defVal := range defaults {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		existing, exists := config[k]
		if !exists {
			config[k] = defVal
			filled[path] = true
			continue
		}

		defMap, defIsMap := defVal.(map[string]interface{})
		existMap, existIsMap := existing.(map[string]interface{})
		if defIsMap && existIsMap && len(defMap) > 0 {
			mergeDefaults(existMap, defMap, path, filled)
		}
	}
}

// MergeJSON is a convenience wrapper that returns the merged result as a
// re-serialized JSON string. Useful for text and JSON output formats.
func MergeJSON(rawJSON string, component string, k8sMinor int) (string, error) {
	result, err := Merge(rawJSON, component, k8sMinor)
	if err != nil {
		return "", err
	}

	if len(result.Filled) == 0 {
		return rawJSON, nil
	}

	out, err := json.Marshal(result.Merged)
	if err != nil {
		return "", fmt.Errorf("re-serializing merged configz: %w", err)
	}

	return string(out), nil
}
