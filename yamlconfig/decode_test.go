package yamlconfig_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

type hasAPISecret struct {
	APISecret time.Duration `yaml:"api_secret"`
}

type hasTimeout struct {
	Timeout time.Duration `yaml:"timeout"`
}

func TestDecodeSensitiveDurationNeverLeaks(t *testing.T) {
	t.Parallel()

	secrets := []string{
		"line1\nline2-secret",
		"tab\tsecret",
		"quote\"secret",
		"back\\slash-secret",
		"ünïcode-secret",
		"plain-secret-value",
	}

	for _, secret := range secrets {
		// Build YAML that quotes the secret so control characters survive.
		escaped := secret
		escaped = strings.ReplaceAll(escaped, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		escaped = strings.ReplaceAll(escaped, "\t", "\\t")
		escaped = strings.ReplaceAll(escaped, "\r", "\\r")
		yaml := "api_secret: \"" + escaped + "\"\n"
		root, err := yamlconfig.Read("test.yaml", []byte(yaml))
		if err != nil {
			t.Fatalf("Read(%q) error = %v", secret, err)
		}

		var dst hasAPISecret
		err = yamlconfig.DecodeValueStrict(root, &dst)
		if !errors.Is(err, ashlar.ErrValidation) {
			t.Errorf("secret %q: error = %v, want ErrValidation", secret, err)
			continue
		}

		errStr := err.Error()
		vStr := fmt.Sprintf("%+v", err)
		// None of these may appear: the raw secret, strconv.Quote(secret)
		// without its surrounding quotes, and %q(secret) without its quotes.
		quoted := strconv.Quote(secret)
		quoted = quoted[1 : len(quoted)-1]
		qFmt := fmt.Sprintf("%q", secret)
		qFmt = qFmt[1 : len(qFmt)-1]
		for _, needle := range []string{secret, quoted, qFmt} {
			if strings.Contains(errStr, needle) || strings.Contains(vStr, needle) {
				t.Errorf("secret %q leaked in error (%q present): %v", secret, needle, err)
			}
		}
	}

	// Non-sensitive control row: timeout with "5 parsecs" still shows the value.
	root, err := yamlconfig.Read("test2.yaml", []byte("timeout: \"5 parsecs\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var timeoutDst hasTimeout
	err = yamlconfig.DecodeValueStrict(root, &timeoutDst)
	if !errors.Is(err, ashlar.ErrValidation) {
		t.Fatalf("non-sensitive error = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), `"5 parsecs"`) {
		t.Errorf("non-sensitive message missing quoted value: %v", err)
	}
}
