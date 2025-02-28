package genapp

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"sort"

	"github.com/goadesign/goa/design"
	"github.com/goadesign/goa/goagen/codegen"
)

// WildcardRegex is the regex used to capture path parameters.
var WildcardRegex = regexp.MustCompile("(?:[^/]*/:([^/]+))+")

type (
	// ContextsWriter generate codes for a goa application contexts.
	ContextsWriter struct {
		*codegen.SourceFile
		CtxTmpl     *template.Template
		CtxNewTmpl  *template.Template
		CtxRespTmpl *template.Template
		PayloadTmpl *template.Template
	}

	// ControllersWriter generate code for a goa application handlers.
	// Handlers receive a HTTP request, create the action context, call the action code and send the
	// resulting HTTP response.
	ControllersWriter struct {
		*codegen.SourceFile
		CtrlTmpl  *template.Template
		MountTmpl *template.Template
	}

	// ResourcesWriter generate code for a goa application resources.
	// Resources are data structures initialized by the application handlers and passed to controller
	// actions.
	ResourcesWriter struct {
		*codegen.SourceFile
		ResourceTmpl *template.Template
	}

	// MediaTypesWriter generate code for a goa application media types.
	// Media types are data structures used to render the response bodies.
	MediaTypesWriter struct {
		*codegen.SourceFile
		MediaTypeTmpl *template.Template
	}

	// UserTypesWriter generate code for a goa application user types.
	// User types are data structures defined in the DSL with "Type".
	UserTypesWriter struct {
		*codegen.SourceFile
		UserTypeTmpl *template.Template
	}

	// ContextTemplateData contains all the information used by the template to render the context
	// code for an action.
	ContextTemplateData struct {
		Name         string // e.g. "ListBottleContext"
		ResourceName string // e.g. "bottles"
		ActionName   string // e.g. "list"
		Params       *design.AttributeDefinition
		Payload      *design.UserTypeDefinition
		Headers      *design.AttributeDefinition
		Routes       []*design.RouteDefinition
		Responses    map[string]*design.ResponseDefinition
		API          *design.APIDefinition
		DefaultPkg   string
	}

	// ControllerTemplateData contains the information required to generate an action handler.
	ControllerTemplateData struct {
		API      *design.APIDefinition    // API definition
		Resource string                   // Lower case plural resource name, e.g. "bottles"
		Actions  []map[string]interface{} // Array of actions, each action has keys "Name", "Routes", "Context" and "Unmarshal"
		Encoders []*EncoderTemplateData   // Encoder data
		Decoders []*EncoderTemplateData   // Decoder data
	}

	// ResourceData contains the information required to generate the resource GoGenerator
	ResourceData struct {
		Name              string                      // Name of resource
		Identifier        string                      // Identifier of resource media type
		Description       string                      // Description of resource
		Type              *design.MediaTypeDefinition // Type of resource media type
		CanonicalTemplate string                      // CanonicalFormat represents the resource canonical path in the form of a fmt.Sprintf format.
		CanonicalParams   []string                    // CanonicalParams is the list of parameter names that appear in the resource canonical path in order.
	}

	// EncoderTemplateData contains the data needed to render the registration code for a single
	// encoder or decoder package.
	EncoderTemplateData struct {
		// PackagePath is the Go package path to the package implmenting the encoder/decoder.
		PackagePath string
		// PackageName is the name of the Go package implementing the encoder/decoder.
		PackageName string
		// Function is the name of the package function implementing the decoder/encoder factory.
		Function string
		// MIMETypes is the list of supported MIME types.
		MIMETypes []string
		// Default is true if this encoder/decoder should be set as the default.
		Default bool
	}
)

// IsPathParam returns true if the given parameter name corresponds to a path parameter for all
// the context action routes. Such parameter is required but does not need to be validated as
// httprouter takes care of that.
func (c *ContextTemplateData) IsPathParam(param string) bool {
	params := c.Params
	pp := false
	if params.Type.IsObject() {
		for _, r := range c.Routes {
			pp = false
			for _, p := range r.Params() {
				if p == param {
					pp = true
					break
				}
			}
			if !pp {
				break
			}
		}
	}
	return pp
}

// MustValidate returns true if code that checks for the presence of the given param must be
// generated.
func (c *ContextTemplateData) MustValidate(name string) bool {
	return c.Params.IsRequired(name) && !c.IsPathParam(name)
}

// IterateResponses iterates through the responses sorted by status code.
func (c *ContextTemplateData) IterateResponses(it func(*design.ResponseDefinition) error) error {
	m := make(map[int]*design.ResponseDefinition, len(c.Responses))
	var s []int
	for _, resp := range c.Responses {
		status := resp.Status
		m[status] = resp
		s = append(s, status)
	}
	sort.Ints(s)
	for _, status := range s {
		if err := it(m[status]); err != nil {
			return err
		}
	}
	return nil
}

// NewContextsWriter returns a contexts code writer.
// Contexts provide the glue between the underlying request data and the user controller.
func NewContextsWriter(filename string) (*ContextsWriter, error) {
	file, err := codegen.SourceFileFor(filename)
	if err != nil {
		return nil, err
	}
	return &ContextsWriter{SourceFile: file}, nil
}

// Execute writes the code for the context types to the writer.
func (w *ContextsWriter) Execute(data *ContextTemplateData) error {
	if err := w.ExecuteTemplate("context", ctxT, nil, data); err != nil {
		return err
	}
	fn := template.FuncMap{
		"newCoerceData":  newCoerceData,
		"arrayAttribute": arrayAttribute,
	}
	if err := w.ExecuteTemplate("new", ctxNewT, fn, data); err != nil {
		return err
	}
	if data.Payload != nil {
		if err := w.ExecuteTemplate("payload", payloadT, nil, data); err != nil {
			return err
		}
	}
	fn = template.FuncMap{
		"project": func(mt *design.MediaTypeDefinition, v string) *design.MediaTypeDefinition {
			p, _, _ := mt.Project(v)
			return p
		},
	}
	data.IterateResponses(func(resp *design.ResponseDefinition) error {
		respData := map[string]interface{}{
			"Context":  data,
			"Response": resp,
		}
		if resp.Type != nil {
			respData["Type"] = resp.Type
			if err := w.ExecuteTemplate("response", ctxTRespT, fn, respData); err != nil {
				return err
			}
		} else if mt := design.Design.MediaTypeWithIdentifier(resp.MediaType); mt != nil {
			respData["MediaType"] = mt
			fn["respName"] = func(resp *design.ResponseDefinition, view string) string {
				if view == "default" {
					return codegen.Goify(resp.Name, true)
				}
				base := fmt.Sprintf("%s%s", resp.Name, strings.Title(view))
				return codegen.Goify(base, true)
			}
			if err := w.ExecuteTemplate("response", ctxMTRespT, fn, respData); err != nil {
				return err
			}
		} else {
			if err := w.ExecuteTemplate("response", ctxNoMTRespT, fn, respData); err != nil {
				return err
			}
		}
		return nil
	})
	return nil
}

// NewControllersWriter returns a handlers code writer.
// Handlers provide the glue between the underlying request data and the user controller.
func NewControllersWriter(filename string) (*ControllersWriter, error) {
	file, err := codegen.SourceFileFor(filename)
	if err != nil {
		return nil, err
	}
	return &ControllersWriter{SourceFile: file}, nil
}

// WriteInitService writes the initService function
func (w *ControllersWriter) WriteInitService(encoders, decoders []*EncoderTemplateData) error {
	ctx := map[string]interface{}{
		"API":      design.Design,
		"Encoders": encoders,
		"Decoders": decoders,
	}
	if err := w.ExecuteTemplate("service", serviceT, nil, ctx); err != nil {
		return err
	}
	return nil
}

// Execute writes the handlers GoGenerator
func (w *ControllersWriter) Execute(data []*ControllerTemplateData) error {
	if len(data) == 0 {
		return nil
	}
	for _, d := range data {
		if err := w.ExecuteTemplate("controller", ctrlT, nil, d); err != nil {
			return err
		}
		if err := w.ExecuteTemplate("mount", mountT, nil, d); err != nil {
			return err
		}
		if err := w.ExecuteTemplate("unmarshal", unmarshalT, nil, d); err != nil {
			return err
		}
	}
	return nil
}

// NewResourcesWriter returns a contexts code writer.
// Resources provide the glue between the underlying request data and the user controller.
func NewResourcesWriter(filename string) (*ResourcesWriter, error) {
	file, err := codegen.SourceFileFor(filename)
	if err != nil {
		return nil, err
	}
	return &ResourcesWriter{SourceFile: file}, nil
}

// Execute writes the code for the context types to the writer.
func (w *ResourcesWriter) Execute(data *ResourceData) error {
	return w.ExecuteTemplate("resource", resourceT, nil, data)
}

// NewMediaTypesWriter returns a contexts code writer.
// Media types contain the data used to render response bodies.
func NewMediaTypesWriter(filename string) (*MediaTypesWriter, error) {
	file, err := codegen.SourceFileFor(filename)
	if err != nil {
		return nil, err
	}
	return &MediaTypesWriter{SourceFile: file}, nil
}

// Execute writes the code for the context types to the writer.
func (w *MediaTypesWriter) Execute(mt *design.MediaTypeDefinition) error {
	var mLinks *design.UserTypeDefinition
	viewMT := mt
	err := mt.IterateViews(func(view *design.ViewDefinition) error {
		p, links, err := mt.Project(view.Name)
		if mLinks == nil {
			mLinks = links
		}
		if err != nil {
			return err
		}
		viewMT = p
		if err := w.ExecuteTemplate("mediatype", mediaTypeT, nil, viewMT); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if mLinks != nil {
		if err := w.ExecuteTemplate("usertype", userTypeT, nil, mLinks); err != nil {
			return err
		}
	}
	return nil
}

// NewUserTypesWriter returns a contexts code writer.
// User types contain custom data structured defined in the DSL with "Type".
func NewUserTypesWriter(filename string) (*UserTypesWriter, error) {
	file, err := codegen.SourceFileFor(filename)
	if err != nil {
		return nil, err
	}
	return &UserTypesWriter{SourceFile: file}, nil
}

// Execute writes the code for the context types to the writer.
func (w *UserTypesWriter) Execute(t *design.UserTypeDefinition) error {
	return w.ExecuteTemplate("types", userTypeT, nil, t)
}

// newCoerceData is a helper function that creates a map that can be given to the "Coerce" template.
func newCoerceData(name string, att *design.AttributeDefinition, pointer bool, pkg string, depth int) map[string]interface{} {
	return map[string]interface{}{
		"Name":      name,
		"VarName":   codegen.Goify(name, false),
		"Pointer":   pointer,
		"Attribute": att,
		"Pkg":       pkg,
		"Depth":     depth,
	}
}

// arrayAttribute returns the array element attribute definition.
func arrayAttribute(a *design.AttributeDefinition) *design.AttributeDefinition {
	return a.Type.(*design.Array).ElemType
}

const (
	// ctxT generates the code for the context data type.
	// template input: *ContextTemplateData
	ctxT = `// {{.Name}} provides the {{.ResourceName}} {{.ActionName}} action context.
type {{.Name}} struct {
	context.Context
	*goa.ResponseData
	*goa.RequestData
{{if .Params}}{{range $name, $att := .Params.Type.ToObject}}{{/*
*/}}	{{goify $name true}} {{if and $att.Type.IsPrimitive ($.Params.IsPrimitivePointer $name)}}*{{end}}{{gotyperef .Type nil 0}}
{{end}}{{end}}{{if .Payload}}	Payload {{gotyperef .Payload nil 0}}
{{end}}}
`
	// coerceT generates the code that coerces the generic deserialized
	// data to the actual type.
	// template input: map[string]interface{} as returned by newCoerceData
	coerceT = `{{if eq .Attribute.Type.Kind 1}}{{/*

*/}}{{/* BooleanType */}}{{/*
*/}}{{$varName := or (and (not .Pointer) .VarName) tempvar}}{{/*
*/}}{{tabs .Depth}}if {{.VarName}}, err2 := strconv.ParseBool(raw{{goify .Name true}}); err2 == nil {
{{if .Pointer}}{{tabs .Depth}}	{{$varName}} := &{{.VarName}}
{{end}}{{tabs .Depth}}	{{.Pkg}} = {{$varName}}
{{tabs .Depth}}} else {
{{tabs .Depth}}	err = goa.InvalidParamTypeError("{{.Name}}", raw{{goify .Name true}}, "boolean", err)
{{tabs .Depth}}}
{{end}}{{if eq .Attribute.Type.Kind 2}}{{/*

*/}}{{/* IntegerType */}}{{/*
*/}}{{$tmp := tempvar}}{{/*
*/}}{{tabs .Depth}}if {{.VarName}}, err2 := strconv.Atoi(raw{{goify .Name true}}); err2 == nil {
{{if .Pointer}}{{$tmp2 := tempvar}}{{tabs .Depth}}	{{$tmp2}} := {{.VarName}}
{{tabs .Depth}}	{{$tmp}} := &{{$tmp2}}
{{tabs .Depth}}	{{.Pkg}} = {{$tmp}}
{{else}}{{tabs .Depth}}	{{.Pkg}} = {{.VarName}}
{{end}}{{tabs .Depth}}} else {
{{tabs .Depth}}	err = goa.InvalidParamTypeError("{{.Name}}", raw{{goify .Name true}}, "integer", err)
{{tabs .Depth}}}
{{end}}{{if eq .Attribute.Type.Kind 3}}{{/*

*/}}{{/* NumberType */}}{{/*
*/}}{{$varName := or (and (not .Pointer) .VarName) tempvar}}{{/*
*/}}{{tabs .Depth}}if {{.VarName}}, err2 := strconv.ParseFloat(raw{{goify .Name true}}, 64); err2 == nil {
{{if .Pointer}}{{tabs .Depth}}	{{$varName}} := &{{.VarName}}
{{end}}{{tabs .Depth}}	{{.Pkg}} = {{$varName}}
{{tabs .Depth}}} else {
{{tabs .Depth}}	err = goa.InvalidParamTypeError("{{.Name}}", raw{{goify .Name true}}, "number", err)
{{tabs .Depth}}}
{{end}}{{if eq .Attribute.Type.Kind 4}}{{/*

*/}}{{/* StringType */}}{{/*
*/}}{{tabs .Depth}}{{.Pkg}} = {{if .Pointer}}&{{end}}raw{{goify .Name true}}
{{end}}{{if eq .Attribute.Type.Kind 5}}{{/*

*/}}{{/* DateTimeType */}}{{/*
*/}}{{$varName := or (and (not .Pointer) .VarName) tempvar}}{{/*
*/}}{{tabs .Depth}}if {{.VarName}}, err2 := time.Parse("RFC3339", raw{{goify .Name true}}); err2 == nil {
{{if .Pointer}}{{tabs .Depth}}	{{$varName}} := &{{.VarName}}
{{end}}{{tabs .Depth}}	{{.Pkg}} = {{$varName}}
{{tabs .Depth}}} else {
{{tabs .Depth}}	err = goa.InvalidParamTypeError("{{.Name}}", raw{{goify .Name true}}, "datetime", err)
{{tabs .Depth}}}
{{end}}{{if eq .Attribute.Type.Kind 6}}{{/*

*/}}{{/* AnyType */}}{{/*
*/}}{{tabs .Depth}}{{.Pkg}} = {{if .Pointer}}&{{end}}raw{{goify .Name true}}
{{end}}{{if eq .Attribute.Type.Kind 7}}{{/*

*/}}{{/* ArrayType */}}{{/*
*/}}{{tabs .Depth}}elems{{goify .Name true}} := strings.Split(raw{{goify .Name true}}, ",")
{{if eq (arrayAttribute .Attribute).Type.Kind 4}}{{tabs .Depth}}{{.Pkg}} = elems{{goify .Name true}}
{{else}}{{tabs .Depth}}elems{{goify .Name true}}2 := make({{gotyperef .Attribute.Type nil .Depth}}, len(elems{{goify .Name true}}))
{{tabs .Depth}}for i, rawElem := range elems{{goify .Name true}} {
{{template "Coerce" (newCoerceData "elem" (arrayAttribute .Attribute) false (printf "elems%s2[i]" (goify .Name true)) (add .Depth 1))}}{{tabs .Depth}}}
{{tabs .Depth}}{{.Pkg}} = elems{{goify .Name true}}2
{{end}}{{end}}`

	// ctxNewT generates the code for the context factory method.
	// template input: *ContextTemplateData
	ctxNewT = `{{define "Coerce"}}` + coerceT + `{{end}}` + `
// New{{goify .Name true}} parses the incoming request URL and body, performs validations and creates the
// context used by the {{.ResourceName}} controller {{.ActionName}} action.
func New{{.Name}}(ctx context.Context) (*{{.Name}}, error) {
	var err error
	req := goa.Request(ctx)
	rctx := {{.Name}}{Context: ctx, ResponseData: goa.Response(ctx), RequestData: req}
{{if .Headers}}{{$headers := .Headers}}{{range $name, $att := $headers.Type.ToObject}}	raw{{goify $name true}} := req.Header.Get("{{$name}}")
{{if $headers.IsRequired $name}}	if raw{{goify $name true}} == "" {
		err = goa.MissingHeaderError("{{$name}}", err)
	} else {
{{else}}	if raw{{goify $name true}} != "" {
{{end}}{{$validation := validationChecker $att ($headers.IsNonZero $name) ($headers.IsRequired $name) (printf "raw%s" (goify $name true)) $name 2}}{{/*
*/}}{{if $validation}}{{$validation}}
{{end}}	}
{{end}}{{end}}{{if.Params}}{{range $name, $att := .Params.Type.ToObject}}	raw{{goify $name true}} := req.Params.Get("{{$name}}")
{{$mustValidate := $.MustValidate $name}}{{if $mustValidate}}	if raw{{goify $name true}} == "" {
		err = goa.MissingParamError("{{$name}}", err)
	} else {
{{else}}	if raw{{goify $name true}} != "" {
{{end}}{{template "Coerce" (newCoerceData $name $att ($.Params.IsPrimitivePointer $name) (printf "rctx.%s" (goify $name true)) 2)}}{{/*
*/}}{{$validation := validationChecker $att ($.Params.IsNonZero $name) ($.Params.IsRequired $name) (printf "rctx.%s" (goify $name true)) $name 2}}{{/*
*/}}{{if $validation}}{{$validation}}
{{end}}	}
{{end}}{{end}}{{/* if .Params */}}	return &rctx, err
}
`
	// ctxMTRespT generates the response helpers for responses with media types.
	// template input: map[string]interface{}
	ctxMTRespT = `{{$ctx := .Context}}{{$resp := .Response}}{{$mt := .MediaType}}{{/*
*/}}{{range $name, $view := $mt.Views}}{{if not (eq $name "link")}}{{$projected := project $mt $name}}
// {{respName $resp $name}} sends a HTTP response with status code {{$resp.Status}}.
func (ctx *{{$ctx.Name}}) {{respName $resp $name}}(r {{gotyperef $projected $projected.AllRequired 0}}) error {
	ctx.ResponseData.Header().Set("Content-Type", "{{$resp.MediaType}}")
	return ctx.ResponseData.Send(ctx.Context, {{$resp.Status}}, r)
}
{{end}}{{end}}
`

	// ctxTRespT generates the response helpers for responses with overridden types.
	// template input: map[string]interface{}
	ctxTRespT = `// {{goify .Response.Name true}} sends a HTTP response with status code {{.Response.Status}}.
func (ctx *{{.Context.Name}}) {{goify .Response.Name true}}(r {{gotyperef .Type nil 0}}) error {
	ctx.ResponseData.Header().Set("Content-Type", "{{.Response.MediaType}}")
	return ctx.ResponseData.Send(ctx.Context, {{.Response.Status}}, r)
}
`

	// ctxNoMTRespT generates the response helpers for responses with no known media type.
	// template input: *ContextTemplateData
	ctxNoMTRespT = `
// {{goify .Response.Name true}} sends a HTTP response with status code {{.Response.Status}}.
func (ctx *{{.Context.Name}}) {{goify .Response.Name true}}({{if .Response.MediaType}}resp []byte{{end}}) error {
{{if .Response.MediaType}}	ctx.ResponseData.Header().Set("Content-Type", "{{.Response.MediaType}}")
{{end}}	ctx.ResponseData.WriteHeader({{.Response.Status}}){{if .Response.MediaType}}
	_, err := ctx.ResponseData.Write(resp)
	return err{{else}}
	return nil{{end}}
}
`

	// payloadT generates the payload type definition GoGenerator
	// template input: *ContextTemplateData
	payloadT = `{{$payload := .Payload}}// {{gotypename .Payload nil 0}} is the {{.ResourceName}} {{.ActionName}} action payload.
type {{gotypename .Payload nil 1}} {{gotypedef .Payload 0 true}}

{{$validation := recursiveValidate .Payload.AttributeDefinition false false "payload" "raw" 1}}{{if $validation}}// Validate runs the validation rules defined in the design.
func (payload {{gotyperef .Payload .Payload.AllRequired 0}}) Validate() (err error) {
{{$validation}}
       return
}{{end}}
`
	// ctrlT generates the controller interface for a given resource.
	// template input: *ControllerTemplateData
	ctrlT = `// {{.Resource}}Controller is the controller interface for the {{.Resource}} actions.
type {{.Resource}}Controller interface {
	goa.Muxer
{{range .Actions}}	{{.Name}}(*{{.Context}}) error
{{end}}}
`

	// serviceT generates the service initialization code.
	// template input: *ControllerTemplateData
	serviceT = `
// inited is true if initService has been called
var inited = false

// initService sets up the service encoders, decoders and mux.
func initService(service *goa.Service) {
	if inited {
		return
	}
	inited = true

	// Setup encoders and decoders
{{range .Encoders}}{{/*
*/}}	service.Encoder({{.PackageName}}.{{.Function}}, "{{join .MIMETypes "\", \""}}")
{{end}}{{range .Decoders}}{{/*
*/}}	service.Decoder({{.PackageName}}.{{.Function}}, "{{join .MIMETypes "\", \""}}")
{{end}}

	// Setup default encoder and decoder
{{range .Encoders}}{{if .Default}}{{/*
*/}}	service.Encoder({{.PackageName}}.{{.Function}}, "*/*")
{{end}}{{end}}{{range .Decoders}}{{if .Default}}{{/*
*/}}	service.Decoder({{.PackageName}}.{{.Function}}, "*/*")
{{end}}{{end}}}
`

	// mountT generates the code for a resource "Mount" function.
	// template input: *ControllerTemplateData
	mountT = `
// Mount{{.Resource}}Controller "mounts" a {{.Resource}} resource controller on the given service.
func Mount{{.Resource}}Controller(service *goa.Service, ctrl {{.Resource}}Controller) {
	initService(service)
	var h goa.Handler
{{$res := .Resource}}{{range .Actions}}{{$action := .}}	h = func(ctx context.Context, rw http.ResponseWriter, req *http.Request) error {
		rctx, err := New{{.Context}}(ctx)
		if err != nil {
			return goa.NewBadRequestError(err)
		}
{{if .Payload}}if rawPayload := goa.Request(ctx).Payload; rawPayload != nil {
			rctx.Payload = rawPayload.({{gotyperef .Payload nil 1}})
		}
		{{end}}		return ctrl.{{.Name}}(rctx)
	}
{{range .Routes}}	service.Mux.Handle("{{.Verb}}", "{{.FullPath}}", ctrl.MuxHandler("{{$action.Name}}", h, {{if $action.Payload}}{{$action.Unmarshal}}{{else}}nil{{end}}))
	service.Info("mount", "ctrl", "{{$res}}", "action", "{{$action.Name}}", "route", "{{.Verb}} {{.FullPath}}")
{{end}}{{end}}}
`

	// unmarshalT generates the code for an action payload unmarshal function.
	// template input: *ControllerTemplateData
	unmarshalT = `{{range .Actions}}{{if .Payload}}
// {{.Unmarshal}} unmarshals the request body into the context request data Payload field.
func {{.Unmarshal}}(ctx context.Context, req *http.Request) error {
	var payload {{gotypename .Payload nil 1}}
	if err := goa.RequestService(ctx).DecodeRequest(req, &payload); err != nil {
		return err
	}{{$validation := recursiveValidate .Payload.AttributeDefinition false false "payload" "raw" 1}}{{if $validation}}
	if err := payload.Validate(); err != nil {
		return err
	}{{end}}
	goa.Request(ctx).Payload = {{if .Payload.IsObject}}&{{end}}payload
	return nil
}
{{end}}
{{end}}`

	// resourceT generates the code for a resource.
	// template input: *ResourceData
	resourceT = `{{if .CanonicalTemplate}}// {{.Name}}Href returns the resource href.
func {{.Name}}Href({{if .CanonicalParams}}{{join .CanonicalParams ", "}} interface{}{{end}}) string {
	return fmt.Sprintf("{{.CanonicalTemplate}}", {{join .CanonicalParams ", "}})
}
{{end}}`

	// mediaTypeT generates the code for a media type.
	// template input: MediaTypeTemplateData
	mediaTypeT = `// {{gotypedesc . true}}
//
// Identifier: {{.Identifier}}{{$typeName := gotypename . .AllRequired 0}}
type {{$typeName}} {{gotypedef . 0 true}}

{{$validation := recursiveValidate .AttributeDefinition false false "mt" "response" 1}}{{if $validation}}// Validate validates the {{$typeName}} media type instance.
func (mt {{gotyperef . .AllRequired 0}}) Validate() (err error) {
{{$validation}}
	return
}
{{end}}
`

	// userTypeT generates the code for a user type.
	// template input: UserTypeTemplateData
	userTypeT = `// {{gotypedesc . true}}{{$typeName := gotypename . .AllRequired 0}}
type {{$typeName}} {{gotypedef . 0 true}}

{{$validation := recursiveValidate .AttributeDefinition false false "ut" "response" 1}}{{if $validation}}// Validate validates the {{$typeName}} type instance.
func (ut {{gotyperef . .AllRequired 0}}) Validate() (err error) {
{{$validation}}
	return
}{{end}}
`
)
