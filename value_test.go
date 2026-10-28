package ashlar_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
)

func TestPresenceDistinguishesOmittedNullAndZeroValues(t *testing.T) {
	t.Parallel()

	prov := ashlar.NewProvenance(ashlar.FileOrigin(ashlar.SourceSpan{
		Source: "config.yaml",
		Start:  ashlar.SourcePos{Line: 1, Column: 1},
	}), false)

	zeroNum, err := ashlar.NumberValue("0", prov)
	if err != nil {
		t.Fatalf("NumberValue(0): %v", err)
	}

	root, err := ashlar.ObjectValue([]ashlar.Member{
		{Key: "explicit_null", Value: ashlar.NullValue(prov)},
		{Key: "explicit_false", Value: ashlar.BoolValue(false, prov)},
		{Key: "explicit_zero", Value: zeroNum},
		{Key: "explicit_empty_str", Value: ashlar.StringValue("", prov)},
		{Key: "explicit_empty_arr", Value: ashlar.ArrayValue(nil, prov)},
		{Key: "explicit_empty_obj", Value: mustObject(t, nil, prov)},
	}, prov)
	if err != nil {
		t.Fatalf("ObjectValue: %v", err)
	}

	cases := []struct {
		ptr      ashlar.Pointer
		wantPres ashlar.Presence
		wantKind ashlar.Kind
	}{
		{ashlar.NewPointer("omitted_key"), ashlar.PresenceOmitted, ashlar.KindInvalid},
		{ashlar.NewPointer("explicit_null"), ashlar.PresenceNull, ashlar.KindNull},
		{ashlar.NewPointer("explicit_false"), ashlar.PresencePresent, ashlar.KindBool},
		{ashlar.NewPointer("explicit_zero"), ashlar.PresencePresent, ashlar.KindNumber},
		{ashlar.NewPointer("explicit_empty_str"), ashlar.PresencePresent, ashlar.KindString},
		{ashlar.NewPointer("explicit_empty_arr"), ashlar.PresencePresent, ashlar.KindArray},
		{ashlar.NewPointer("explicit_empty_obj"), ashlar.PresencePresent, ashlar.KindObject},
	}

	for _, tc := range cases {
		got := root.At(tc.ptr)
		if got.Presence() != tc.wantPres {
			t.Errorf("At(%q).Presence() = %s, want %s", tc.ptr, got.Presence(), tc.wantPres)
		}
		if root.PresenceAt(tc.ptr) != tc.wantPres {
			t.Errorf("PresenceAt(%q) = %s, want %s", tc.ptr, root.PresenceAt(tc.ptr), tc.wantPres)
		}
		if got.Kind() != tc.wantKind {
			t.Errorf("At(%q).Kind() = %s, want %s", tc.ptr, got.Kind(), tc.wantKind)
		}
	}

	if b, ok := root.At(ashlar.NewPointer("explicit_false")).Bool(); !ok || b {
		t.Errorf("explicit_false Bool() = %v, %v; want false, true", b, ok)
	}
	if s, ok := root.At(ashlar.NewPointer("explicit_empty_str")).StringVal(); !ok || s != "" {
		t.Errorf("explicit_empty_str StringVal() = %q, %v; want \"\", true", s, ok)
	}
}

func TestNumberLexemeFidelity(t *testing.T) {
	t.Parallel()

	prov := ashlar.NewProvenance(ashlar.DefaultOrigin("num"), false)
	validLexemes := []string{
		"0",
		"-0",
		"1",
		"1.0",
		"1.50",
		"1e0",
		"1E+05",
		"1.25e-3",
		"-42",
		"9223372036854775807",
		"18446744073709551615",
		"123456789012345678901234567890",
	}

	for _, lex := range validLexemes {
		v, err := ashlar.NumberValue(lex, prov)
		if err != nil {
			t.Fatalf("NumberValue(%q) unexpected error: %v", lex, err)
		}
		got, ok := v.NumberLexeme()
		if !ok || got != lex {
			t.Errorf("NumberLexeme() = %q, %v; want exact %q", got, ok, lex)
		}
		jn, ok := v.JSONNumber()
		if !ok || jn.String() != lex {
			t.Errorf("JSONNumber() = %q, %v; want %q", jn, ok, lex)
		}
	}

	invalidLexemes := []string{
		"",
		"-",
		"+1",
		"01",
		"-01",
		"1.",
		".5",
		"1e",
		"1e+",
		"NaN",
		"Infinity",
		"1_000",
	}
	for _, bad := range invalidLexemes {
		if _, err := ashlar.NumberValue(bad, prov); !errors.Is(err, ashlar.ErrSyntax) {
			t.Errorf("NumberValue(%q) error = %v, want ErrSyntax", bad, err)
		}
	}
}

func TestObjectDuplicateKeyRejectedWithSourceLocation(t *testing.T) {
	t.Parallel()

	firstOrig := ashlar.FileOrigin(ashlar.SourceSpan{Source: "manifest.json", Start: ashlar.SourcePos{Line: 3, Column: 3}})
	dupOrig := ashlar.FileOrigin(ashlar.SourceSpan{Source: "manifest.json", Start: ashlar.SourcePos{Line: 7, Column: 3}})
	prov := ashlar.NewProvenance(firstOrig, false)

	_, err := ashlar.ObjectValue([]ashlar.Member{
		{Key: "name", KeyOrigin: firstOrig, Value: ashlar.StringValue("first", prov)},
		{Key: "name", KeyOrigin: dupOrig, Value: ashlar.StringValue("second", prov)},
	}, prov)

	if !errors.Is(err, ashlar.ErrDuplicateKey) {
		t.Fatalf("ObjectValue duplicate key error = %v, want ErrDuplicateKey", err)
	}
	var aerr *ashlar.Error
	if !errors.As(err, &aerr) {
		t.Fatalf("errors.As(err, &aerr) = false")
	}
	if got, want := aerr.Origin.Location(), "manifest.json:7:3"; got != want {
		t.Errorf("error Origin.Location() = %q, want %q", got, want)
	}
	if !strings.Contains(aerr.Error(), "manifest.json:3:3") {
		t.Errorf("error %q should mention first definition at manifest.json:3:3", aerr.Error())
	}
}

func TestValueAndProvenanceImmutability(t *testing.T) {
	t.Parallel()

	prov := ashlar.NewProvenance(ashlar.DefaultOrigin("root"), false)
	elems := []ashlar.Value{ashlar.StringValue("a", prov), ashlar.StringValue("b", prov)}
	arr := ashlar.ArrayValue(elems, prov)

	// Mutate caller slice and returned Elements() slice.
	elems[0] = ashlar.StringValue("mutated", prov)
	gotElems := arr.Elements()
	gotElems[1] = ashlar.StringValue("mutated2", prov)

	if s, _ := arr.Index(0).StringVal(); s != "a" {
		t.Errorf("arr.Index(0) = %q, want \"a\"", s)
	}
	if s, _ := arr.Index(1).StringVal(); s != "b" {
		t.Errorf("arr.Index(1) = %q, want \"b\"", s)
	}
	// Leading-zero index "01" must return Omitted per RFC 6901.
	if !arr.At(ashlar.Pointer("/01")).IsOmitted() {
		t.Error("At(\"/01\") must be Omitted per RFC 6901")
	}

	members := []ashlar.Member{{Key: "k", Value: arr}}
	obj := mustObject(t, members, prov)
	members[0].Key = "mutated"
	gotMembers := obj.Members()
	gotMembers[0].Key = "mutated2"

	if keys := obj.Keys(); len(keys) != 1 || keys[0] != "k" {
		t.Errorf("obj.Keys() = %v, want [\"k\"]", keys)
	}
}

func TestProvenanceOverrideHistoryAndRedaction(t *testing.T) {
	t.Parallel()

	r := ashlar.DefaultRedactor()

	// 1. Non-sensitive setting: default -> file -> env -> explicit
	timeoutPtr := ashlar.NewPointer("timeout")
	vDefault := ashlar.StringValue("5s", ashlar.NewProvenance(ashlar.DefaultOrigin("timeout"), false))
	vFile := ashlar.StringValue("12s", ashlar.NewProvenance(ashlar.FileOrigin(ashlar.SourceSpan{
		Source: "config.yaml",
		Start:  ashlar.SourcePos{Line: 6, Column: 10},
	}), false))
	vEnv := ashlar.StringValue("30s", ashlar.NewProvenance(ashlar.EnvOrigin("APP_TIMEOUT"), false))
	vFlag := ashlar.StringValue("45s", ashlar.NewProvenance(ashlar.ExplicitOrigin("--timeout"), false))

	eff := vDefault.
		OverrideWith(vFile, r, timeoutPtr).
		OverrideWith(vEnv, r, timeoutPtr).
		OverrideWith(vFlag, r, timeoutPtr)

	wantExplain := `explicit:--timeout = "45s" (overrode env:APP_TIMEOUT = "30s", config.yaml:6:10 = "12s", default:timeout = "5s")`
	if got := eff.Explain(r, timeoutPtr); got != wantExplain {
		t.Errorf("eff.Explain() =\n  %s\nwant:\n  %s", got, wantExplain)
	}

	// 2. Sensitive setting: sensitive values must never appear in Summary, Superseded, or Explain.
	tokenPtr := ashlar.NewPointer("api", "auth", "tokens", "0", "token")
	const fileSensitiveVal = "file-raw-val-111"
	const envSensitiveVal = "env-raw-val-222"

	sFile := ashlar.StringValue(fileSensitiveVal, ashlar.NewProvenance(ashlar.FileOrigin(ashlar.SourceSpan{
		Source: "auth.yaml",
		Start:  ashlar.SourcePos{Line: 4, Column: 9},
	}), false))
	sEnv := ashlar.StringValue(envSensitiveVal, ashlar.NewProvenance(ashlar.EnvOrigin("APP_AUTH_HEADER"), false))

	sEff := sFile.OverrideWith(sEnv, r, tokenPtr)
	explained := sEff.Explain(r, tokenPtr)

	if strings.Contains(explained, fileSensitiveVal) || strings.Contains(explained, envSensitiveVal) {
		t.Fatalf("sensitive value leaked in Provenance.Explain(): %s", explained)
	}
	wantRedacted := "env:APP_AUTH_HEADER = [REDACTED] (overrode auth.yaml:4:9 = [REDACTED])"
	if explained != wantRedacted {
		t.Errorf("sEff.Explain() = %q, want %q", explained, wantRedacted)
	}
}

func TestParseByteSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    int64
		wantErr bool
	}{
		{"1024", 1024, false},
		{"512KB", 512 * 1024, false},
		{"64mb", 64 * 1024 * 1024, false},
		{"2GB", 2 * 1024 * 1024 * 1024, false},
		{"", 0, true},
		{"0", 0, true},
		{"-10MB", 0, true},
		{"not-a-sizeKB", 0, true},
		{"9999999999999999GB", 0, true},
	}

	for _, tc := range cases {
		got, err := ashlar.ParseByteSize(tc.raw)
		if tc.wantErr {
			if !errors.Is(err, ashlar.ErrValidation) {
				t.Errorf("ParseByteSize(%q) error = %v, want ErrValidation", tc.raw, err)
			}
			if tc.raw != "" && !strings.Contains(err.Error(), tc.raw) {
				t.Errorf("ParseByteSize(%q) error %q must preserve original input string", tc.raw, err.Error())
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d, nil", tc.raw, got, err, tc.want)
		}
	}
}

func mustObject(t *testing.T, members []ashlar.Member, prov ashlar.Provenance) ashlar.Value {
	t.Helper()
	v, err := ashlar.ObjectValue(members, prov)
	if err != nil {
		t.Fatalf("ObjectValue: %v", err)
	}
	return v
}
