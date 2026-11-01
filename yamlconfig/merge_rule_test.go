package yamlconfig_test

import (
	"testing"

	"github.com/mattjoyce/ashlar/yamlconfig"
)

// TestMergeRuleString checks the name of every MergeRule, including one
// outside the defined set.
func TestMergeRuleString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		r    yamlconfig.MergeRule
		want string
	}{
		{yamlconfig.MergeUnspecified, "unspecified"},
		{yamlconfig.MergeReplace, "replace"},
		{yamlconfig.MergeMap, "map"},
		{yamlconfig.MergeAppendSlice, "append_slice"},
		{yamlconfig.MergeForbidDuplicate, "forbid_duplicate"},
		{yamlconfig.MergeRule(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("MergeRule(%d).String() = %q, want %q", int(tc.r), got, tc.want)
		}
	}
}
