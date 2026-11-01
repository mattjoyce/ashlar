package jsoncontract_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// TestReadSyntaxErrorsWithholdTokenContent checks that malformed JSON reports
// the offending token's kind and location but never its content, which may
// be a secret.
func TestReadSyntaxErrorsWithholdTokenContent(t *testing.T) {
	t.Parallel()

	const secret = "sekrit-123"
	cases := []struct {
		name string
		json string
		kind error
		loc  string
	}{
		{"trailing_string", `{} "` + secret + `"`, ashlar.ErrTrailingContent, "contract.json:1:4"},
		{"string_where_colon_expected", `{"a" "` + secret + `"}`, ashlar.ErrSyntax, "contract.json:1:6"},
		{"bare_word_value", `{"v":` + secret + `}`, ashlar.ErrSyntax, "contract.json:1:6"},
		{"bad_escape", `{"v":"\` + secret + `"}`, ashlar.ErrSyntax, "contract.json:1:6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsoncontract.Read("contract.json", []byte(tc.json))
			var aErr *ashlar.Error
			if !errors.As(err, &aErr) || !errors.Is(err, tc.kind) {
				t.Fatalf("Read() error = %v, want %v", err, tc.kind)
			}
			if got := aErr.Origin.Location(); got != tc.loc {
				t.Errorf("Origin.Location() = %q, want %q", got, tc.loc)
			}
			for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
				if got := fmt.Sprintf(format, err); strings.Contains(got, secret) {
					t.Errorf("format %q shows %q: %s", format, secret, got)
				}
			}
		})
	}
}
