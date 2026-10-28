# relayd: embedding Ashlar in a service

A telemetry relay with two distinct input boundaries:

1. **Runtime configuration (`yamlconfig` + `schema`):** `config.yaml` includes `routes.yaml` and `plugins.yaml`. Defaults, included YAML fragments, `RELAYD_*` environment variables, `${VAR}` interpolation, and `--set` CLI overrides compose at the `ashlar.Value` node level under declared rules before validating against `config.schema.json` and decoding into Go structs.
2. **Quarantined data contracts (`jsoncontract` + `schema`):** `manifest.json` is a single-document JSON contract read and validated against `manifest.schema.json` without includes, merging, environment expansion, or default insertion, preserving original document bytes and exact numeric spellings (`1`, `1.0`, `1e0`, `18446744073709551615`).

Start with the package comment in `config.go`; it gives the reading order. Then:

```sh
go run .                                   # resolved snapshot, provenance report, and manifest summary
go run . /service/tick_interval            # provenance chain for one setting
go run . /auth/signing_secret              # redacted provenance chain for a secret
go run . --set /service/max_workers=32     # explicit CLI override layered at the top of precedence
go test ./...
```

## What the example shows

- **Composition is declared, not implicit.** `Policy()` in `config.go` sets `/routes` to `MergeAppendSlice` so `routes.yaml` extends `config.yaml`, and `/plugins/*` to `MergeForbidDuplicate` so two fragments cannot silently redefine the same plugin.
- **Provenance is part of the value.** Every resolved leaf reports its winning origin (`default`, `file:line:column (included via ...)`, `env:VAR`, `explicit:--set`) and the ordered history of values it overrode.
- **Secrets never leak, and safe references stay visible.** `auth.signing_secret` is redacted (`[REDACTED]`) in provenance reports and conversion errors, while `auth.token_ref` (`vault://relayd/ingest-token`) remains visible because Ashlar's default redactor exempts `_ref`, `_file`, `_path`, `_dir`, `_sha256`, and `_hash` suffixes.
- **Data contracts stay quarantined from runtime config.** `LoadManifest` in `load.go` verifies that `manifest.schema.json` is closed (`schema.VerifyClosed`), validates `manifest.json`, preserves `RawBytes()` for canonical SHA-256 digesting, and distinguishes `PresenceOmitted` (`sampling_rate`) from `PresencePresent` zero (`compression_level: 0`) and `PresenceNull` (`notes: null`).
