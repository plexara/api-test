package oapi

import (
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

// BuildOptions controls Document generation.
type BuildOptions struct {
	Info    Info
	Servers []Server
	// APIKeyHeader is the inbound API-key header name. When set, an
	// apiKey security scheme is emitted under components and applied
	// to every route whose EndpointMeta.AuthRequired is true.
	APIKeyHeader string
	// BearerEnabled adds an HTTP Bearer security scheme alongside the
	// API-key scheme; either credential type satisfies AuthRequired
	// routes.
	BearerEnabled bool
}

// Build assembles a Document from the registry's routes.
//
// Each EndpointMeta produces one Operation under paths[path][method].
// Path parameters are extracted from "{name}" segments in the path and
// typed via PathParams when supplied (defaulting to string). Query
// parameters come from QueryParams' struct fields. Request and response
// shapes come from RequestBody and ResponseBody respectively; both
// default to application/json.
func Build(reg *endpoints.Registry, opts BuildOptions) Document {
	doc := Document{
		OpenAPI: "3.1.0",
		Info:    opts.Info,
		Servers: opts.Servers,
		Paths:   map[string]PathItem{},
	}

	doc.Components = componentsFor(opts)
	doc.Tags = tagsFor(reg)

	for _, group := range reg.Groups() {
		for _, route := range group.Routes() {
			pathItem := doc.Paths[route.Path]
			op := operationFor(route, opts)
			assignOperation(&pathItem, route.Method, op)
			doc.Paths[route.Path] = pathItem
		}
	}
	return doc
}

func componentsFor(opts BuildOptions) *Components {
	schemes := map[string]SecurityScheme{}
	if opts.APIKeyHeader != "" {
		schemes["apiKey"] = SecurityScheme{
			Type:        "apiKey",
			In:          "header",
			Name:        opts.APIKeyHeader,
			Description: "Inbound API key header. Configured by api_keys.header_name.",
		}
	}
	if opts.BearerEnabled {
		schemes["bearer"] = SecurityScheme{
			Type:        "http",
			Scheme:      "bearer",
			Description: "Inbound bearer token validated against the static bearer.tokens list or the OIDC validator when enabled.",
		}
	}
	if len(schemes) == 0 {
		return nil
	}
	return &Components{SecuritySchemes: schemes}
}

func tagsFor(reg *endpoints.Registry) []Tag {
	seen := map[string]bool{}
	tags := make([]Tag, 0, len(reg.Groups()))
	for _, g := range reg.Groups() {
		name := g.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		tags = append(tags, Tag{Name: name})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
	return tags
}

func operationFor(route endpoints.EndpointMeta, opts BuildOptions) *Operation {
	op := &Operation{
		OperationID: route.Name,
		Description: route.Description,
		Tags:        []string{route.Group},
		Parameters:  parametersFor(route),
		Responses:   responsesFor(route),
	}
	if route.RequestBody != nil {
		op.RequestBody = &RequestBody{
			Required: true,
			Content: map[string]MediaType{
				"application/json": {Schema: schemaFromType(reflect.TypeOf(route.RequestBody))},
			},
		}
	}
	if route.AuthRequired {
		op.Security = securityRequirement(opts)
	}
	return op
}

// pathParamPattern matches "{name}" segments in a route path. Cross-segment
// patterns ("{name...}") are not supported by the registry today, mirroring
// matchSegments() in pkg/endpoints/registry.go.
var pathParamPattern = regexp.MustCompile(`\{([^}/]+)\}`)

func parametersFor(route endpoints.EndpointMeta) []Parameter {
	var params []Parameter

	for _, m := range pathParamPattern.FindAllStringSubmatch(route.Path, -1) {
		name := m[1]
		schema := pathParamSchema(name, route.PathParams)
		params = append(params, Parameter{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   schema,
		})
	}

	if route.QueryParams != nil {
		t := reflect.TypeOf(route.QueryParams)
		for t != nil && t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t != nil && t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				name, omitEmpty, skip := parseJSONTag(f)
				if skip {
					continue
				}
				params = append(params, Parameter{
					Name:     name,
					In:       "query",
					Required: !omitEmpty,
					Schema:   schemaFromType(f.Type),
				})
			}
		}
	}
	return params
}

func pathParamSchema(name string, pathParams any) *Schema {
	if pathParams == nil {
		return &Schema{Type: "string"}
	}
	t := reflect.TypeOf(pathParams)
	if ft, ok := lookupFieldByJSONName(t, name); ok {
		return schemaFromType(ft)
	}
	return &Schema{Type: "string"}
}

func responsesFor(route endpoints.EndpointMeta) map[string]Response {
	resp := map[string]Response{}
	if route.ResponseBody != nil {
		resp["200"] = Response{
			Description: "Success",
			Content: map[string]MediaType{
				"application/json": {Schema: schemaFromType(reflect.TypeOf(route.ResponseBody))},
			},
		}
	} else {
		resp["200"] = Response{Description: "Success"}
	}
	return resp
}

func securityRequirement(opts BuildOptions) []map[string][]string {
	var out []map[string][]string
	if opts.APIKeyHeader != "" {
		out = append(out, map[string][]string{"apiKey": {}})
	}
	if opts.BearerEnabled {
		out = append(out, map[string][]string{"bearer": {}})
	}
	return out
}

func assignOperation(p *PathItem, method string, op *Operation) {
	switch strings.ToUpper(method) {
	case "GET":
		p.Get = op
	case "POST":
		p.Post = op
	case "PUT":
		p.Put = op
	case "PATCH":
		p.Patch = op
	case "DELETE":
		p.Delete = op
	case "HEAD":
		p.Head = op
	case "OPTIONS":
		p.Options = op
	}
}
