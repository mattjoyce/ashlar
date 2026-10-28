package ashlar_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
)

func TestSourceCoordinatesAndOrigin(t *testing.T) {
	t.Parallel()

	pos := ashlar.SourcePos{Line: 14, Column: 7, Offset: 128}
	if !pos.Valid() {
		t.Fatal("SourcePos.Valid() = false, want true")
	}
	if got, want := pos.String(), "14:7"; got != want {
		t.Errorf("SourcePos.String() = %q, want %q", got, want)
	}
	if (ashlar.SourcePos{}).Valid() || (ashlar.SourcePos{}).String() != "" {
		t.Error("zero SourcePos should be invalid and render empty string")
	}

	span := ashlar.SourceSpan{Source: "config.yaml", Start: pos, End: ashlar.SourcePos{Line: 14, Column: 18}}
	if got, want := span.String(), "config.yaml:14:7"; got != want {
		t.Errorf("SourceSpan.String() = %q, want %q", got, want)
	}
	if got, want := (ashlar.SourceSpan{Source: "config.yaml"}).String(), "config.yaml"; got != want {
		t.Errorf("file-only SourceSpan.String() = %q, want %q", got, want)
	}
	if got, want := (ashlar.SourceSpan{Start: pos}).String(), "14:7"; got != want {
		t.Errorf("pos-only SourceSpan.String() = %q, want %q", got, want)
	}

	via1 := ashlar.SourceSpan{Source: "root.yaml", Start: ashlar.SourcePos{Line: 3, Column: 5}}
	via2 := ashlar.SourceSpan{Source: "sub/routes.yaml", Start: ashlar.SourcePos{Line: 9, Column: 2}}
	viaSlice := []ashlar.SourceSpan{via1, via2}

	fileOrig := ashlar.FileOrigin(span, viaSlice...)
	// Mutate caller slice and returned Via() slice to verify immutability.
	viaSlice[0].Source = "mutated.yaml"
	gotVia := fileOrig.Via()
	gotVia[0].Source = "mutated-again.yaml"

	if got, want := fileOrig.Via()[0].Source, "root.yaml"; got != want {
		t.Errorf("Origin.Via()[0].Source = %q, want %q (defensive copy required)", got, want)
	}
	wantOrigStr := "config.yaml:14:7 (included via root.yaml:3:5 -> sub/routes.yaml:9:2)"
	if got := fileOrig.String(); got != wantOrigStr {
		t.Errorf("Origin.String() = %q, want %q", got, wantOrigStr)
	}

	origins := []struct {
		origin ashlar.Origin
		want   string
	}{
		{ashlar.DefaultOrigin("timeout"), "default:timeout"},
		{ashlar.DefaultOrigin(""), "default"},
		{ashlar.EnvOrigin("ANAM_TIMEOUT"), "env:ANAM_TIMEOUT"},
		{ashlar.ExplicitOrigin("--server"), "explicit:--server"},
	}
	for _, tc := range origins {
		if got := tc.origin.String(); got != tc.want {
			t.Errorf("Origin.String() = %q, want %q", got, tc.want)
		}
	}
}

func TestPointerRFC6901(t *testing.T) {
	t.Parallel()

	p := ashlar.NewPointer("plugins", "a/b~c", "0", "api_key")
	if got, want := p.String(), "/plugins/a~1b~0c/0/api_key"; got != want {
		t.Errorf("NewPointer() = %q, want %q", got, want)
	}
	if !p.Valid() {
		t.Fatalf("Pointer(%q).Valid() = false, want true", p)
	}
	if got, want := p.TopToken(), "plugins"; got != want {
		t.Errorf("TopToken() = %q, want %q", got, want)
	}
	if got, want := p.LastToken(), "api_key"; got != want {
		t.Errorf("LastToken() = %q, want %q", got, want)
	}

	tokens, err := p.Tokens()
	if err != nil {
		t.Fatalf("Tokens() unexpected error: %v", err)
	}
	wantTokens := []string{"plugins", "a/b~c", "0", "api_key"}
	if len(tokens) != len(wantTokens) {
		t.Fatalf("Tokens() len = %d, want %d", len(tokens), len(wantTokens))
	}
	for i := range wantTokens {
		if tokens[i] != wantTokens[i] {
			t.Errorf("Tokens()[%d] = %q, want %q", i, tokens[i], wantTokens[i])
		}
	}

	child := ashlar.RootPointer.Child("routes").Index(2).Child("from")
	if got, want := child.String(), "/routes/2/from"; got != want {
		t.Errorf("Child/Index pointer = %q, want %q", got, want)
	}

	invalid := []string{
		"no-leading-slash",
		"/bad~escape",
		"/trailing~",
		"/~2",
	}
	for _, raw := range invalid {
		if _, err := ashlar.ParsePointer(raw); !errors.Is(err, ashlar.ErrPointer) {
			t.Errorf("ParsePointer(%q) error = %v, want ErrPointer", raw, err)
		}
	}
}

func TestErrorTaxonomyAndAggregation(t *testing.T) {
	t.Parallel()

	span := ashlar.SourceSpan{
		Source: "manifests/heart-rate.json",
		Start:  ashlar.SourcePos{Line: 22, Column: 9},
	}
	orig := ashlar.FileOrigin(span)

	dupErr := ashlar.NewError(
		ashlar.ErrDuplicateKey,
		ashlar.NewPointer("schema", "properties", "bpm"),
		orig,
		`duplicate object key "bpm"`,
		nil,
	)

	if !errors.Is(dupErr, ashlar.ErrDuplicateKey) {
		t.Error("errors.Is(dupErr, ErrDuplicateKey) = false, want true")
	}
	if !errors.Is(dupErr, ashlar.ErrSyntax) {
		t.Error("errors.Is(dupErr, ErrSyntax) = false, want true (parent sentinel)")
	}
	if errors.Is(dupErr, ashlar.ErrSchema) {
		t.Error("errors.Is(dupErr, ErrSchema) = true, want false")
	}

	var structured *ashlar.Error
	if !errors.As(dupErr, &structured) {
		t.Fatal("errors.As(dupErr, &structured) = false, want true")
	}
	if structured.Origin.Location() != "manifests/heart-rate.json:22:9" {
		t.Errorf("Origin.Location() = %q, want manifests/heart-rate.json:22:9", structured.Origin.Location())
	}

	unknownErr := ashlar.NewError(
		ashlar.ErrUnknownField,
		ashlar.NewPointer("ledger_pth"),
		ashlar.FileOrigin(ashlar.SourceSpan{Source: "config.yaml", Start: ashlar.SourcePos{Line: 4, Column: 1}}),
		`unknown field "ledger_pth"`,
		nil,
	)
	if !errors.Is(unknownErr, ashlar.ErrUnknownField) || !errors.Is(unknownErr, ashlar.ErrSchema) {
		t.Error("unknownErr should match both ErrUnknownField and ErrSchema")
	}

	cycleErr := ashlar.NewError(
		ashlar.ErrIncludeCycle,
		ashlar.NewPointer("include", "0"),
		ashlar.FileOrigin(ashlar.SourceSpan{Source: "b.yaml", Start: ashlar.SourcePos{Line: 2, Column: 3}}),
		"circular include a.yaml -> b.yaml -> a.yaml",
		nil,
	)
	if !errors.Is(cycleErr, ashlar.ErrIncludeCycle) || !errors.Is(cycleErr, ashlar.ErrComposition) {
		t.Error("cycleErr should match both ErrIncludeCycle and ErrComposition")
	}

	joined := ashlar.JoinErrors(nil, dupErr, ashlar.JoinErrors(unknownErr, cycleErr))
	if !errors.Is(joined, ashlar.ErrDuplicateKey) || !errors.Is(joined, ashlar.ErrUnknownField) || !errors.Is(joined, ashlar.ErrIncludeCycle) {
		t.Error("joined error must unwrap all constituent errors for errors.Is")
	}

	var multi *ashlar.Errors
	if !errors.As(joined, &multi) {
		t.Fatal("errors.As(joined, &multi) = false, want true")
	}
	if got := len(multi.List()); got != 3 {
		t.Errorf("len(multi.List()) = %d, want 3", got)
	}
}

func TestSecretRedactionNeverLeaks(t *testing.T) {
	t.Parallel()

	r := ashlar.DefaultRedactor()

	sensitiveCases := []struct {
		ptr  ashlar.Pointer
		key  string
		want bool
	}{
		{ashlar.NewPointer("api", "auth", "tokens", "0", "token"), "token", true},
		{ashlar.NewPointer("plugins", "withings", "config", "client_secret"), "client_secret", true},
		{ashlar.NewPointer("plugins", "discord", "config", "bot-api-key"), "bot-api-key", true},
		{ashlar.NewPointer("db", "password"), "password", true},
		{ashlar.NewPointer("tls", "private_key"), "private_key", true},
		// Safe references, file paths, and hashes must NOT be classified as raw secrets:
		{ashlar.NewPointer("api", "auth", "tokens", "0", "secret_ref"), "secret_ref", false},
		{ashlar.NewPointer("token_file"), "token_file", false},
		{ashlar.NewPointer("secrets", "age_key_file"), "age_key_file", false},
		{ashlar.NewPointer("principals", "0", "token_sha256"), "token_sha256", false},
		{ashlar.NewPointer("service", "tick_interval"), "tick_interval", false},
	}

	for _, tc := range sensitiveCases {
		if got := r.IsSensitive(tc.ptr, tc.key); got != tc.want {
			t.Errorf("DefaultRedactor().IsSensitive(%q, %q) = %v, want %v", tc.ptr, tc.key, got, tc.want)
		}
	}

	const rawSecret = "super-secret-bearer-value-998877"
	underlyingCause := fmt.Errorf("invalid token format for %q", rawSecret)
	ptr := ashlar.NewPointer("api", "auth", "tokens", "0", "token")
	orig := ashlar.FileOrigin(ashlar.SourceSpan{Source: "config.yaml", Start: ashlar.SourcePos{Line: 19, Column: 12}})

	redactedErr := ashlar.NewRedactedError(
		ashlar.ErrValidation,
		ptr,
		orig,
		fmt.Sprintf("rejected value %q", rawSecret),
		underlyingCause,
		r,
		rawSecret,
		false,
	)

	if !redactedErr.Sensitive {
		t.Fatal("redactedErr.Sensitive = false, want true")
	}

	renderedForms := []string{
		redactedErr.Error(),
		fmt.Sprintf("%v", redactedErr),
		fmt.Sprintf("%+v", redactedErr),
		fmt.Sprintf("%s", redactedErr),
		fmt.Sprintf("%q", redactedErr),
	}
	for _, form := range renderedForms {
		if strings.Contains(form, rawSecret) {
			t.Fatalf("secret leaked in formatted error: %s", form)
		}
		if !strings.Contains(form, ashlar.RedactedLiteral) {
			t.Errorf("formatted error %q does not contain %q", form, ashlar.RedactedLiteral)
		}
	}

	if cause := redactedErr.Cause(); cause != nil && strings.Contains(cause.Error(), rawSecret) {
		t.Fatalf("secret leaked in Cause().Error(): %s", cause.Error())
	}

	// Verify SecretFingerprint is deterministic, domain-separated, and never echoes rawSecret.
	fp1 := ashlar.SecretFingerprint("/api/auth/tokens/0/token", rawSecret)
	fp2 := ashlar.SecretFingerprint("/api/auth/tokens/0/token", rawSecret)
	fpOtherPurpose := ashlar.SecretFingerprint("/webhooks/0/secret", rawSecret)
	if fp1 != fp2 {
		t.Errorf("SecretFingerprint not deterministic: %q != %q", fp1, fp2)
	}
	if fp1 == fpOtherPurpose {
		t.Error("SecretFingerprint must separate domains by purpose")
	}
	if !strings.HasPrefix(fp1, "secretfp_sha256:") || strings.Contains(fp1, rawSecret) {
		t.Errorf("unexpected SecretFingerprint format: %q", fp1)
	}
}
