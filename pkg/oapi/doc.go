// Package oapi generates an OpenAPI 3.1 document from the api-test
// endpoint registry. The same EndpointMeta values the portal renders are
// reflected over here to produce the spec served at /openapi.json and
// /openapi.yaml.
//
// The generator is intentionally minimal: it covers the shapes the
// shipped endpoint groups actually use, not the full OpenAPI surface.
// New shapes get added as endpoint groups need them. Boot-time
// self-check (selfcheck.go) prevents the served mux from drifting
// from the published document.
package oapi

// Document is the top-level OpenAPI 3.1 object.
type Document struct {
	OpenAPI    string                `json:"openapi" yaml:"openapi"`
	Info       Info                  `json:"info" yaml:"info"`
	Servers    []Server              `json:"servers,omitempty" yaml:"servers,omitempty"`
	Paths      map[string]PathItem   `json:"paths" yaml:"paths"`
	Components *Components           `json:"components,omitempty" yaml:"components,omitempty"`
	Security   []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
	Tags       []Tag                 `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Info is the OpenAPI info object.
type Info struct {
	Title       string `json:"title" yaml:"title"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string `json:"version" yaml:"version"`
}

// Server is one entry in the servers list.
type Server struct {
	URL         string `json:"url" yaml:"url"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Tag is one entry in the top-level tags list.
type Tag struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// PathItem groups operations registered against a single path.
type PathItem struct {
	Get     *Operation `json:"get,omitempty" yaml:"get,omitempty"`
	Post    *Operation `json:"post,omitempty" yaml:"post,omitempty"`
	Put     *Operation `json:"put,omitempty" yaml:"put,omitempty"`
	Patch   *Operation `json:"patch,omitempty" yaml:"patch,omitempty"`
	Delete  *Operation `json:"delete,omitempty" yaml:"delete,omitempty"`
	Head    *Operation `json:"head,omitempty" yaml:"head,omitempty"`
	Options *Operation `json:"options,omitempty" yaml:"options,omitempty"`
}

// Operation is one (method, path) endpoint.
type Operation struct {
	OperationID string                `json:"operationId" yaml:"operationId"`
	Summary     string                `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty" yaml:"tags,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses" yaml:"responses"`
	Security    []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
}

// Parameter is a path or query parameter on an operation.
type Parameter struct {
	Name        string  `json:"name" yaml:"name"`
	In          string  `json:"in" yaml:"in"`
	Required    bool    `json:"required,omitempty" yaml:"required,omitempty"`
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Schema      *Schema `json:"schema,omitempty" yaml:"schema,omitempty"`
}

// RequestBody is an operation's request body declaration.
type RequestBody struct {
	Description string               `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                 `json:"required,omitempty" yaml:"required,omitempty"`
	Content     map[string]MediaType `json:"content" yaml:"content"`
}

// Response is an entry in operation.responses.
type Response struct {
	Description string               `json:"description" yaml:"description"`
	Content     map[string]MediaType `json:"content,omitempty" yaml:"content,omitempty"`
}

// MediaType holds a schema for one content-type variant.
type MediaType struct {
	Schema *Schema `json:"schema,omitempty" yaml:"schema,omitempty"`
}

// Components holds reusable definitions referenced from operations.
type Components struct {
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
}

// SecurityScheme describes one authentication mechanism.
type SecurityScheme struct {
	Type        string `json:"type" yaml:"type"`
	In          string `json:"in,omitempty" yaml:"in,omitempty"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Scheme      string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Schema is the JSON Schema subset the generator emits. OpenAPI 3.1
// aligns with JSON Schema 2020-12; this struct covers the subset the
// reflector produces. Additional fields can be added as needed.
type Schema struct {
	Type                 string             `json:"type,omitempty" yaml:"type,omitempty"`
	Format               string             `json:"format,omitempty" yaml:"format,omitempty"`
	Description          string             `json:"description,omitempty" yaml:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty" yaml:"properties,omitempty"`
	Required             []string           `json:"required,omitempty" yaml:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty" yaml:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
	Nullable             bool               `json:"nullable,omitempty" yaml:"nullable,omitempty"`
	Example              any                `json:"example,omitempty" yaml:"example,omitempty"`
	Enum                 []any              `json:"enum,omitempty" yaml:"enum,omitempty"`
}
