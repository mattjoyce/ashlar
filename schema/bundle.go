package schema

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Bundle compiles one or more self-contained JSON Schema (Draft 2020-12)
// resources offline. External network $ref resolution is always disabled.
type Bundle struct {
	resources []resourceEntry
}

type resourceEntry struct {
	uri string
	doc jsoncontract.Document
	raw any
}

// NewBundle creates an offline JSON Schema (Draft 2020-12) bundle.
func NewBundle() *Bundle {
	return &Bundle{}
}

// AddResource parses rawSchema strictly via jsoncontract.Read and registers it
// in the bundle under uri (or its top-level "$id" when uri is empty).
func (b *Bundle) AddResource(uri string, rawSchema []byte) error {
	source := uri
	if source == "" {
		source = "schema.json"
	}
	doc, err := jsoncontract.Read(source, rawSchema)
	if err != nil {
		return err
	}
	return b.AddDocument(uri, doc)
}

// AddDocument registers an already-parsed jsoncontract.Document in the bundle
// under uri (or its top-level "$id" when uri is empty).
func (b *Bundle) AddDocument(uri string, doc jsoncontract.Document) error {
	raw, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.RawBytes()))
	if err != nil {
		return ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.RootPointer,
			doc.Root().Provenance().Effective,
			fmt.Sprintf("decode schema JSON %q", doc.Source()),
			err,
		)
	}
	regURI := strings.TrimSpace(uri)
	if regURI == "" {
		if id, ok := doc.Root().At(ashlar.NewPointer("$id")).StringVal(); ok && id != "" {
			regURI = id
		}
	}
	if regURI == "" {
		return ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.NewPointer("$id"),
			doc.Root().Provenance().Effective,
			"schema resource requires a non-empty URI or top-level \"$id\"",
			nil,
		)
	}
	b.resources = append(b.resources, resourceEntry{
		uri: regURI,
		doc: doc,
		raw: raw,
	})
	return nil
}

// Compile compiles the schema at targetURI (which may include a JSON Pointer
// fragment anchor such as "https://example.org/config.schema.json#/$defs/WholeConfig").
func (b *Bundle) Compile(targetURI string) (*Validator, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{})

	for _, res := range b.resources {
		if err := c.AddResource(res.uri, res.raw); err != nil {
			return nil, ashlar.NewError(
				ashlar.ErrSchema,
				ashlar.RootPointer,
				res.doc.Root().Provenance().Effective,
				fmt.Sprintf("register schema resource %q", res.uri),
				err,
			)
		}
	}

	sch, err := c.Compile(targetURI)
	if err != nil {
		return nil, ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.RootPointer,
			ashlar.Origin{},
			fmt.Sprintf("compile schema %q", targetURI),
			err,
		)
	}
	return &Validator{
		targetURI: targetURI,
		compiled:  sch,
	}, nil
}

var (
	valueKeywords = map[string]bool{
		"examples": true,
		"default":  true,
		"enum":     true,
		"const":    true,
	}
	namedSchemaMaps = map[string]bool{
		"properties":        true,
		"patternProperties": true,
		"$defs":             true,
		"dependentSchemas":  true,
	}
)

// VerifyClosed inspects a parsed JSON Schema Value tree and verifies that every
// object subschema explicitly sets "additionalProperties" to false or a schema
// object, returning a source-located *ashlar.Error wrapping ashlar.ErrSchema if
// any object subschema is left open.
func VerifyClosed(schemaRoot ashlar.Value) error {
	return verifyClosedAt(ashlar.RootPointer, schemaRoot)
}

func verifyClosedAt(ptr ashlar.Pointer, node ashlar.Value) error {
	switch node.Kind() {
	case ashlar.KindObject:
		if isObjectSubschema(node) {
			if err := checkAdditionalProps(ptr, node); err != nil {
				return err
			}
		}
		for _, m := range node.Members() {
			if valueKeywords[m.Key] {
				continue
			}
			childPtr := ptr.Child(m.Key)
			if namedSchemaMaps[m.Key] && m.Value.Kind() == ashlar.KindObject {
				for _, named := range m.Value.Members() {
					if err := verifyClosedAt(childPtr.Child(named.Key), named.Value); err != nil {
						return err
					}
				}
				continue
			}
			if err := verifyClosedAt(childPtr, m.Value); err != nil {
				return err
			}
		}
	case ashlar.KindArray:
		for i, elem := range node.Elements() {
			if err := verifyClosedAt(ptr.Index(i), elem); err != nil {
				return err
			}
		}
	}
	return nil
}

func isObjectSubschema(node ashlar.Value) bool {
	typeVal := node.At(ashlar.NewPointer("type"))
	if s, ok := typeVal.StringVal(); ok && s == "object" {
		return true
	}
	if typeVal.Kind() == ashlar.KindArray {
		for _, el := range typeVal.Elements() {
			if s, ok := el.StringVal(); ok && s == "object" {
				return true
			}
		}
	}
	return node.At(ashlar.NewPointer("properties")).Kind() == ashlar.KindObject
}

func checkAdditionalProps(ptr ashlar.Pointer, node ashlar.Value) error {
	ap := node.At(ashlar.NewPointer("additionalProperties"))
	if ap.Kind() == ashlar.KindObject {
		return nil
	}
	if b, ok := ap.Bool(); ok && !b {
		return nil
	}
	return ashlar.NewError(
		ashlar.ErrSchema,
		ptr.Child("additionalProperties"),
		node.Provenance().Effective,
		"object schema must set additionalProperties to false or a schema object",
		nil,
	)
}
