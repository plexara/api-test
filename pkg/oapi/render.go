package oapi

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// RenderJSON returns the OpenAPI document as pretty-printed JSON.
func RenderJSON(doc Document) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

// RenderYAML returns the OpenAPI document as YAML.
func RenderYAML(doc Document) ([]byte, error) {
	return yaml.Marshal(doc)
}
