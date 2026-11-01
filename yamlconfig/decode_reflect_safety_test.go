package yamlconfig_test

import (
	"testing"

	"github.com/mattjoyce/ashlar"
)

// selfEmbedding embeds a pointer to its own type.
type selfEmbedding struct {
	*selfEmbedding
	Name string `yaml:"name"`
}

// SelfEmbeddingExported embeds a pointer to its own type through an exported
// embedded field.
type SelfEmbeddingExported struct {
	*SelfEmbeddingExported
	Name string `yaml:"name"`
}

// inlineInner is promoted through an unexported, named inline field.
type inlineInner struct {
	X int `yaml:"x"`
}

// unexportedInline tags an unexported named field inline. It cannot be set
// through reflection, so it must be ignored, not panic.
type unexportedInline struct {
	inner inlineInner `yaml:",inline"`
	Y     int         `yaml:"y"`
}

// TestDecodeSelfEmbeddingTerminates checks that a struct embedding a pointer
// to itself decodes or fails cleanly instead of recursing forever, directly,
// inside a slice, and as an array target the decoder rejects.
func TestDecodeSelfEmbeddingTerminates(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name: "unexported_self_embed", doc: "name: a\n",
			newDst: func() any { return new(selfEmbedding) },
			want:   &selfEmbedding{selfEmbedding: nil, Name: "a"},
		},
		{
			name: "exported_self_embed", doc: "name: a\n",
			newDst: func() any { return new(SelfEmbeddingExported) },
			want:   &SelfEmbeddingExported{Name: "a"},
		},
		{
			name: "self_embed_in_slice", doc: "- name: a\n",
			newDst: func() any { return new([]SelfEmbeddingExported) },
			want:   &[]SelfEmbeddingExported{{Name: "a"}},
		},
		{
			name: "self_embed_unknown_key", doc: "name: a\nbogus: 1\n",
			newDst: func() any { return new(SelfEmbeddingExported) },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/bogus", loc: "config.yaml:2:1"},
		},
		{
			name: "self_embed_array_rejected", doc: "- {}\n",
			newDst: func() any { return new([1]SelfEmbeddingExported) },
			err:    validationAt("", "config.yaml:1:1", "unsupported scalar type"),
		},
	})
}

// TestDecodeUnexportedInlineFieldIsIgnored checks that an inline tag on an
// unexported named field leaves its keys unknown rather than panicking.
func TestDecodeUnexportedInlineFieldIsIgnored(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name: "exported_sibling_decodes", doc: "y: 2\n",
			newDst: func() any { return new(unexportedInline) },
			want:   &unexportedInline{inner: inlineInner{}, Y: 2},
		},
		{
			name: "unexported_inline_key_unknown", doc: "x: 1\n",
			newDst: func() any { return new(unexportedInline) },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/x", loc: "config.yaml:1:1"},
		},
	})
}
