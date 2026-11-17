# homerun2-radio-pitcher

A radio station for the homerun2 LED matrix. Between notifications the
64x64 panel plays a programme: a time check, tech news, facts about the
infrastructure. Notifications are the breaking news. They always cut in.

The radio-pitcher is the **control room**. It works through a schedule and,
when a slot comes due, it builds the segment and pitches it as a homerun2
message into its own Redis stream, `ambient`. It does not handle priority.
The [led-catcher](https://github.com/stuttgart-things/homerun2-led-catcher)
(v0.31.0 or later) does. A display rule with `ambient: true` shows the
programme only while the panel is free. A busy panel drops it rather than
queueing it, and any notification pre-empts it at once (see the led-catcher's
`docs/profile-reference.md`, "Ambient displays").

Background and the concept are in
[homerun-library#146](https://github.com/stuttgart-things/homerun-library/issues/146).

```
schedule.yaml ──► director ──► segment ──► homerun-library ──► Redis stream "ambient" ──► led-catcher
  (git)          (each minute)  time-check   Enqueue(…, "ambient")                          rule ambient: true
                                news ◄── RSS/Atom feeds
                                infra-facts ◄── Kubernetes / Flux / Harvester
                                    └── optional: local Ollama rewords, never invents numbers
```

## Message format

Each programme item is one homerun2 message. The led-catcher renders it as a
`card`.

| Field | Value |
|---|---|
| `system` | `radio` (`RADIO_SYSTEM`) |
| `severity` | `INFO` (`RADIO_SEVERITY`) |
| `title` | short and fitted to the card: by default 4 lines of 16 characters (the catcher's 4x6 font), shortened at a word boundary with `…` |
| `message` | the scrolling line, at most 120 characters (`RADIO_MESSAGE_MAX`) |
| `tags` | `segment=<name>`, e.g. `segment=news`. A catcher rule can filter on it with `tags: [segment=news]` |
| `author` | `radio` (`RADIO_AUTHOR`), the card's footer |
| `timestamp` | when the item was pitched, RFC 3339 |

Example:

```json
{"title":"It's 15:00","message":"Tuesday, 17 November 2026","severity":"INFO",
 "author":"radio","system":"radio","tags":"segment=time-check","timestamp":"2026-11-17T15:00:00+01:00"}
```

The matching rule in the led-catcher profile, placed **above** the wildcard
rules:

```yaml
displayRules:
  radio:
    systems: [radio]
    severity: [INFO]
    kind: card
    strip_prefix: true
    duration: 12
    ambient: true
```

The catcher keeps only the newest ambient card. When a slot has several
items (`count: 3`), the pitcher therefore spaces them by `RADIO_ITEM_SPACING`
(default 15s, a little longer than the card's `duration`).

## Schedule (`schedule.yaml`)

The schedule lives in git and is changed by PR. When the file changes (for
example after a ConfigMap update), the pitcher re-reads it without a restart.
If a new version is broken, the error is logged and `/ready` reports it, and
the last good version stays on air.

```yaml
# clock: the format clock, repeats every hour. Key: minute of the hour
# (":00" to ":59"). `segment` names the segment, every other key is a
# parameter of it.
clock:
  ":00": { segment: time-check }
  ":05": { segment: news, count: 3 }
  ":15": { segment: infra-facts, count: 2 }
  ":20": { segment: news, count: 2, feeds: [cncf] }
  ":30": { segment: time-check }
  ":35": { segment: news, count: 2 }
  ":45": { segment: infra-facts, count: 2, sources: [harvester] }
  ":50": { segment: news, count: 2 }
  # Between slots the catcher shows its idle clock. The pitcher does not air
  # this entry; it documents what the panel shows.
  default: { segment: idle-clock, style: rotate }

# programme: time windows of the day. Parsed and validated, not aired yet
# (follow-up). A window that ends before it starts crosses midnight.
programme:
  "07:00-10:00": { show: morning-show, extra: [weather, calendar] }
  "10:00-17:00": { show: daytime }
  "17:00-19:00": { show: after-work, extra: [daily-summary] }
  "19:00-07:00": { show: night, brightness: 20 }
```

Validation reports every problem at once: a minute outside `:00`–`:59`, the
same minute twice, a slot without `segment`, `count` outside 1–10, a malformed
window, a show without a name, or a brightness outside 0–100. A slot naming a
segment that does not exist (yet), such as `tabletennis-stats`, is logged and
skipped. It is not an error, so the schedule can be written ahead of the code.

Check a schedule before you merge it:

```bash
RADIO_SCHEDULE_PATH=schedule.yaml RADIO_SOURCES_PATH=sources.yaml homerun2-radio-pitcher validate
task validate-schedule SCHEDULE=examples/schedule.yaml
```

The full examples are [`examples/schedule.yaml`](examples/schedule.yaml) and
[`examples/sources.yaml`](examples/sources.yaml).

## Segments

| Segment | Parameters | Airs |
|---|---|---|
| `time-check` | – | `It's 15:00` / `Tuesday, 17 November 2026` in `RADIO_TIMEZONE` |
| `news` | `count` (1), `feeds` (names, default all) | the newest headlines of the feeds not aired in the last `RADIO_NEWS_SEEN_FOR`, younger than `RADIO_NEWS_MAX_AGE`. The title is the headline shortened to fit the card; the message is `<feed>: <full headline>` |
| `infra-facts` | `count` (1), `sources` (names, default all) | randomly picked facts. The last `RADIO_FACTS_RECENT` picks are avoided while other facts are available |

A segment with nothing to say (every headline seen, every source down) airs
nothing, and the panel keeps its idle clock.

### Infra facts and sources (`sources.yaml`)

Every number comes from a live query when the segment airs. A failing source
is logged and skipped; the others still report.

```yaml
sources:
  - name: homerun2-dev        # how the source is called on air
    type: kubernetes          # kubernetes | flux | harvester
    # kubeconfig: empty = in-cluster ServiceAccount
  - name: homerun2-dev
    type: flux
    facts: [kustomizations]   # subset; empty = all facts of the type
  - name: harvester
    type: harvester
    kubeconfig: /etc/homerun2-radio-pitcher/kubeconfigs/harvester
    context: ""               # empty = current context
  - name: platform
    type: kubernetes
    kubeconfig: /etc/homerun2-radio-pitcher/kubeconfigs/platform
    enabled: false            # switched off
```

| Type | Fact | Title / message (fixed text) |
|---|---|---|
| `kubernetes` | `pods` | `312 pods on dev` / `dev: 312 pods, 298 of them running` |
| | `nodes` | `dev: 3 nodes` / `dev: 3 of 3 nodes ready` |
| | `namespaces` | `dev: 41 namespaces` / `dev has 41 namespaces` |
| `flux` | `kustomizations` | `Flux Kustomizations: 41/42 ready` / `dev: 41 of 42 Flux Kustomizations ready, 1 not ready` |
| | `helmreleases` | same for HelmReleases (`helm.toolkit.fluxcd.io/v2`) |
| `harvester` | `vms` | `17 VMs on harvester` / `harvester: 17 VMs, 12 of them running` (`virtualmachines.kubevirt.io/v1`, `status.printableStatus`) |
| | `oldest-vm` | `Oldest VM: db` / `harvester: the oldest VM is db, 400 days old` |

A Flux kind with no objects yields no fact.

#### RBAC for the kubeconfigs

Only `list` is needed, cluster-wide, and nothing else. No `get`, no Secrets,
no write.

| Type | apiGroups | resources | verbs |
|---|---|---|---|
| `kubernetes` | `""` | `pods`, `nodes`, `namespaces` (only those in `facts`) | `list` |
| `flux` | `kustomize.toolkit.fluxcd.io`, `helm.toolkit.fluxcd.io` | `kustomizations`, `helmreleases` | `list` |
| `harvester` | `kubevirt.io` | `virtualmachines` | `list` |

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: homerun2-radio-reader
rules:
  - { apiGroups: [""], resources: [pods, nodes, namespaces], verbs: [list] }
  - { apiGroups: [kustomize.toolkit.fluxcd.io], resources: [kustomizations], verbs: [list] }
  - { apiGroups: [helm.toolkit.fluxcd.io], resources: [helmreleases], verbs: [list] }
  - { apiGroups: [kubevirt.io], resources: [virtualmachines], verbs: [list] }
```

Bind it to the identity in each kubeconfig, for example a ServiceAccount
token. For sources without a kubeconfig (in-cluster), the KCL option
`config.inClusterReadAccess: true` renders this ClusterRole and binds it to
the pitcher's ServiceAccount.

## Moderation (optional, local Ollama)

Off by default. All texts then come from fixed templates. With
`RADIO_LLM_ENABLED=true`, a local Ollama does two things:

- shortens news headlines that do not fit the card (a headline that fits is
  never sent to the model)
- rewords the message line of an infra fact

**The numbers never come from the model.** An answer is rejected, and the
fixed text airs instead, when the answer:

- contains a number that is not in the input (a changed, computed or
  invented number)
- is empty, or is too long for the card or for `RADIO_MESSAGE_MAX`
- times out or errors

Only the first line of an answer counts; quotes and markdown are stripped.
Use the local Ollama in the lab rather than OpenRouter. The shared free key
runs into `429 free-models-per-day`.

## Configuration (environment)

| Variable | Default | |
|---|---|---|
| `REDIS_ADDR` | `localhost` | |
| `REDIS_PORT` | `6379` | |
| `REDIS_PASSWORD` | | |
| `REDIS_STREAM` | `ambient` | target stream |
| `RADIO_SCHEDULE_PATH` | `/etc/homerun2-radio-pitcher/schedule.yaml` | |
| `RADIO_SOURCES_PATH` | | `sources.yaml`; unset = infra-facts stays silent |
| `RADIO_TIMEZONE` | `Europe/Berlin` | the schedule's clock and the time check (zone data is embedded) |
| `RADIO_SYSTEM` | `radio` | message `system` |
| `RADIO_SEVERITY` | `INFO` | message `severity` |
| `RADIO_AUTHOR` | `radio` | message `author` |
| `RADIO_ITEM_SPACING` | `15s` | pause between the items of one slot |
| `RADIO_TITLE_LINES` / `RADIO_TITLE_WIDTH` | `4` / `16` | title space; `3` / `12` keeps titles in the catcher's larger 5x7 font |
| `RADIO_MESSAGE_MAX` | `120` | scrolling line, characters |
| `RADIO_NEWS_FEEDS` | | `name=url,name=url` (RSS 2.0 or Atom); an entry without `name=` is named after its host |
| `RADIO_NEWS_TIMEOUT` | `10s` | per feed |
| `RADIO_NEWS_SEEN_FOR` | `12h` | an aired headline is not repeated within this |
| `RADIO_NEWS_MAX_AGE` | `48h` | older headlines are skipped (undated ones are kept) |
| `RADIO_SOURCE_TIMEOUT` | `10s` | per source query |
| `RADIO_FACTS_RECENT` | `4` | recently aired facts avoided |
| `RADIO_LLM_ENABLED` | `false` | moderation switch |
| `OLLAMA_URL` | | e.g. `http://ollama.ollama.svc.cluster.local:11434` |
| `OLLAMA_MODEL` | | e.g. `llama3.2:3b` |
| `OLLAMA_API` | `openai` | `openai` (`/v1/chat/completions`) or `native` (`/api/chat`) |
| `OLLAMA_TIMEOUT` | `20s` | per call |
| `RADIO_DRY_RUN` | `false` | print messages as JSON lines instead of pitching |
| `PORT` | `8080` | |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | `text`; `debug` also logs why a model answer was rejected |

A wrong value (an unknown time zone, a malformed duration, LLM enabled
without URL or model) stops the start with an error that names the variable.

## Commands

```bash
homerun2-radio-pitcher serve                    # default: air the schedule, serve HTTP
homerun2-radio-pitcher once news count=2        # air one segment now
homerun2-radio-pitcher once infra-facts -dry-run   # print instead of pitch
homerun2-radio-pitcher validate                 # check schedule.yaml and sources.yaml
homerun2-radio-pitcher version
```

`once` is useful for trying a segment on the real panel. On the cluster
(the image has no shell, but the binary runs):

```bash
kubectl -n homerun2 exec deploy/homerun2-radio-pitcher -- /ko-app/homerun2-radio-pitcher once time-check
```

## HTTP

| Path | |
|---|---|
| `GET /health` | liveness and version |
| `GET /ready` | 200 when a schedule is loaded and Redis answers. The body names the schedule state (and a stale-schedule error), Redis, and `lastAired` |
| `GET /metrics` | Prometheus; `homerun2_radio_items_total{segment,result}` |

## Deployment

The release publishes the image `ghcr.io/stuttgart-things/homerun2-radio-pitcher:<tag>`
and a kustomize base rendered from `kcl/`:
`ghcr.io/stuttgart-things/homerun2-radio-pitcher-kustomize:<tag>`. See
[kcl/README.md](kcl/README.md) for the options and
[`tests/kcl-deploy-profile.yaml`](tests/kcl-deploy-profile.yaml) for a full
profile.

Run **one replica only**. Two directors would air every slot twice. The
Deployment uses `strategy: Recreate`, and the KCL schema rejects
`replicas > 1`.

## Development

```bash
go test ./...                       # unit tests; the Redis test skips without REDIS_ADDR
task test                           # all tests with redis-stack (Dagger)
task build-test-binary              # binary against redis-stack: /health, /ready, once → ambient
task lint
task dry-run SEGMENT=news           # needs RADIO_NEWS_FEEDS
task render-manifests-quick
```

Tests never touch a real cluster. Sources run against client-go's fake
clients, feeds against `httptest`, and the model against fakes.
