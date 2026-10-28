package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// main prints the resolved snapshot, explains one JSON Pointer, or applies a --set override.
//
// With no arguments it loads the current directory's configuration and manifest, prints
// the full redacted provenance report (every leaf value, its winning origin, and every
// prior layer it overrode), and prints the quarantined JSON manifest's byte digest,
// exact number lexemes, and field presence.
//
// With one JSON Pointer argument (such as /service/tick_interval or /auth/signing_secret)
// it prints the provenance chain for that single path.
//
// With "--set /pointer=value" it applies an explicit override at the top of the
// precedence order and prints the resulting provenance chain for that pointer.
func main() {
	ctx := context.Background()
	fsys := os.DirFS(".")
	baseOpts := LoadOptions{EnvLookup: defaultExampleEnv}

	switch len(os.Args) {
	case 1:
		g, err := NewGuard(ctx, fsys, baseOpts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		printSnapshot(g.Current())
	case 2:
		g, err := NewGuard(ctx, fsys, baseOpts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := explainPointer(g.Current(), os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case 3:
		if os.Args[1] != "--set" {
			usage()
		}
		if err := runWithSetOverride(ctx, fsys, baseOpts, os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: relayd [/pointer | --set /pointer=value]")
	os.Exit(2)
}

// defaultExampleEnv supplies a deterministic environment when the caller has not exported
// RELAYD_SIGNING_SECRET or RELAYD_SERVICE_TICK_INTERVAL in their shell, so `go run .` works
// out of the box and demonstrates both ${VAR} interpolation and UPPER_SNAKE overrides.
func defaultExampleEnv(key string) (string, bool) {
	if v, ok := os.LookupEnv(key); ok {
		return v, true
	}
	switch key {
	case "RELAYD_SIGNING_SECRET":
		return "hmac-live-secret-do-not-print", true
	case "RELAYD_SERVICE_TICK_INTERVAL":
		return "30s", true
	default:
		return "", false
	}
}

func printSnapshot(s Snapshot) {
	sourceHash := s.Resolved.SourceHash()
	fmt.Printf("service       %s (%s, tick=%s, workers=%d)\n",
		s.Config.Service.Name,
		s.Config.Service.ListenAddr,
		s.Config.Service.TickInterval,
		s.Config.Service.MaxWorkers,
	)
	fmt.Printf("fragments     %s (%s)\n\n", strings.Join(s.Resolved.Files(), " -> "), sourceHash)
	fmt.Println("provenance:")
	for _, line := range strings.Split(s.Resolved.FormatProvenanceReport(), "\n") {
		fmt.Printf("  %s\n", line)
	}

	root := s.Contract.Root()
	ratioLex, _ := root.At("/calibration_ratio").NumberLexeme()
	scaleLex, _ := root.At("/gain_scale").NumberLexeme()
	watermarkLex, _ := root.At("/high_watermark").NumberLexeme()

	fmt.Printf("\nmanifest      %s (%s)\n", s.Manifest.StreamID, ContractDigest(s.Contract))
	fmt.Printf("  lexemes     calibration_ratio=%s  gain_scale=%s  high_watermark=%s\n",
		ratioLex, scaleLex, watermarkLex)
	fmt.Printf("  presence    compression_level=%s  sampling_rate=%s  notes=%s\n",
		presenceLabel(root.PresenceAt("/compression_level")),
		presenceLabel(root.PresenceAt("/sampling_rate")),
		presenceLabel(root.PresenceAt("/notes")),
	)
}

func explainPointer(s Snapshot, rawPtr string) error {
	ptr, err := ashlar.ParsePointer(rawPtr)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", ptr, s.Resolved.Explain(ptr))
	return nil
}

func runWithSetOverride(ctx context.Context, fsys fs.FS, opts LoadOptions, expr string) error {
	initial, err := LoadSnapshot(ctx, fsys, opts)
	if err != nil {
		return err
	}
	ov, err := ParseSetFlag(expr, initial.Resolved.Root())
	if err != nil {
		return err
	}
	opts.Explicit = append(opts.Explicit, ov)
	updated, err := LoadSnapshot(ctx, fsys, opts)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", ov.Pointer, updated.Resolved.Explain(ov.Pointer))
	return nil
}

func presenceLabel(p ashlar.Presence) string {
	switch p {
	case ashlar.PresenceOmitted:
		return "omitted"
	case ashlar.PresenceNull:
		return "null"
	case ashlar.PresencePresent:
		return "present"
	default:
		return "unknown"
	}
}
