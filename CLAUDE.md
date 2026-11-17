# CLAUDE.md

## Project

homerun2-radio-pitcher: plays a radio programme on the homerun2 LED matrix.
It airs the segments of a schedule (`schedule.yaml`) into the Redis stream
`ambient`. The led-catcher (v0.31.0+, rule `ambient: true`) shows them only
on a free panel. Concept: stuttgart-things/homerun-library#146.

## Ground rules

- The director is the control room; the catcher is a dumb renderer. Priority
  is the catcher's job, never the pitcher's timing.
- **Numbers never come from the model.** Moderation (local Ollama, off by
  default) only rewords and shortens; `moderation.NumbersFrom` rejects any
  answer with a number that is not in the input, and every segment has a
  fixed-text fallback.
- Everything is configured by env and YAML; nothing environment-specific in
  code.
- One language: English for code, docs, schedule keys, segment names and
  on-air texts.
- One replica: two directors air every slot twice.

## Layout

- `main.go`: `serve` (default), `once <segment> [k=v] [-dry-run]`,
  `validate`, `version`; wiring in `newStation`
- `internal/config`: env → `Config`, all errors at once
- `internal/schedule`: `schedule.yaml` (`clock`, named `clocks`, `programme`
  with shows and extras), `Due` (extras, then the show's clock slot),
  `ShowAt`, `Watcher` (reload on change, keeps the last good version)
- `internal/director`: minute ticker, once per minute, skips a slot while
  the previous one is on air, spaces the items of a slot, builds the
  `homerun.Message`
- `internal/segment`: `Segment` interface and `Registry`; `time-check`,
  `news` (own RSS/Atom parser, seen-store with TTL), `infra-facts` (random
  pick, avoids recent)
- `internal/sources`: `Source` interface, `sources.yaml`; `Kubernetes`
  (typed client), `Flux` and `Harvester` (dynamic client)
- `internal/moderation`: `Ollama` (OpenAI-compatible or native API),
  `Moderator` with checks and fallback
- `internal/panel`: card title fitting (word wrap like the catcher)
- `internal/pitcher`: `Redis` (homerun-library `Pitcher.Enqueue` with the
  stream override), `Writer` (dry run)
- `internal/handlers`: `/health`, `/ready`, `/metrics`, `Mount` (UI under
  `BASE_PATH`, probes at the root)
- `internal/ui`: programme UI (htmx, vendored `static/htmx.min.js`),
  preview (`segment.Preview` ctx: no state change), portal manifest and
  fragments (homerun2-portal contract, `hr-*` classes); session controls
  behind `RADIO_UI_TOKEN` (`control.go`: cookie + `HX-Request`, or bearer)
- `internal/director/overrides.go`: pause, skip, clock switch, air now; in
  memory, expiring, applied in `Tick` and `Next`
- `kcl/`: Deployment, ConfigMap (schedule + sources), optional Redis Secret,
  optional ClusterRole for in-cluster sources; profile in
  `tests/kcl-deploy-profile.yaml`
- `dagger/`: lint, govulncheck, test (with redis-stack), build-and-test-binary,
  ko build, trivy

## Adding a segment

1. Implement `segment.Segment` in `internal/segment/<name>.go`. Return no
   items when there is nothing to say, and an error only when it failed.
2. Register it in `newStation` and in `validate` (`main.go`).
3. Test it with fakes, document it in the README table, and add it to
   `examples/schedule.yaml` if it should air by default.

## Tech Stack

- Go 1.26+, stdlib `net/http`, `log/slog`
- `homerun-library/v4` (v4.6.0+) for the message model and pitching
- client-go for sources, `go.yaml.in/yaml/v3`
- ko (no Dockerfile), Dagger, Taskfile, semantic-release, renovate

## Git Workflow

Branch per issue, PR, merge. Conventional commits (`feat:`, `fix:`, `test:`,
`docs:`, `chore:`), `Closes #<n>`. Branches `feat/<n>-<desc>`,
`fix/<n>-<desc>`.

## Testing

```bash
go test ./...                 # unit tests, no Redis or cluster needed
task test                     # incl. the Redis integration test (Dagger + redis-stack)
task build-test-binary
task lint
```
