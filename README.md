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
| `tags` | `segment=<name>`, e.g. `segment=news`, then the slot's `card-tags` (e.g. `segment=news,show=news`). A catcher rule can filter on them with `tags: [segment=news]` |
| `url` | the article of a news card (the feed's link); empty for the other segments |
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
# clock: the default format clock, repeats every hour. The key is the minute
# of the hour (":00" to ":59"), `segment` names the segment, every other key
# is a parameter of it. A minute without a slot leaves the panel to the
# catcher's idle clock. It airs outside of every show and in shows without
# their own clock. A minute may also hold a list of slots (a block, e.g. a
# news show): they air one after the other.
#   Every slot takes the director's parameters besides its segment's own:
#   spacing    pause between its items and before the next slot of the
#              block (Go duration, 1s-2m; default RADIO_ITEM_SPACING)
#   card-tags  extra message tags (key=value), e.g. [show=news]: a catcher
#              rule with `tags: [show=news]` and a longer `duration` holds
#              these cards longer. The display time is the catcher's; keep
#              spacing a few seconds above it.
clock:
  ":00": { segment: time-check }
  ":05": { segment: news, count: 2 }
  ":15": { segment: infra-facts, count: 2 }
  ":30": { segment: time-check }
  ":35": { segment: news, count: 2 }
  ":45": { segment: infra-facts, count: 2 }
  # a news show: one block, longer cards
  ":50":
    - { segment: news, count: 3, mix: true, spacing: 23s, card-tags: [show=news] }
    - { segment: infra-facts, count: 1, spacing: 23s, card-tags: [show=news] }
  # Between slots the catcher shows its idle clock. Not aired by the pitcher;
  # documents what the panel shows.
  default: { segment: idle-clock, style: rotate }

# clocks: named clocks for shows, same format. An empty clock is silence:
# the panel stays with the catcher's idle screen.
clocks:
  morning:
    ":00": { segment: time-check }
    ":05": { segment: news, count: 3 }
    ":15": { segment: infra-facts, count: 1 }
    ":20": { segment: news, count: 2, feeds: [cncf] }
    ":30": { segment: time-check }
    ":35": { segment: news, count: 2 }
    ":50": { segment: news, count: 2 }
  night: {}

# programme: the shows of the day. A window that ends before it starts
# crosses midnight; windows must not overlap, and a time outside every
# window airs the default clock.
#   clock: a name from clocks; empty = the default clock
#   extra: segments aired once per window, before the clock slot of the
#          same minute: a name (airs at the start of the window) or
#          { segment: ..., at: "HH:MM", <params> }. Segments that do not
#          exist yet are logged and skipped.
#   brightness: parsed, not aired (dimming belongs to the catcher)
programme:
  "07:00-10:00": { show: morning-show, clock: morning, extra: [weather, calendar] }
  "10:00-17:00": { show: daytime }
  "17:00-19:00":
    show: after-work
    extra:
      - { segment: daily-summary, at: "17:30" }
  "19:00-07:00": { show: night, clock: night, brightness: 20 }
```

At 07:00 the morning show starts: `weather` and `calendar` (its extras), then
the `:00` slot of the `morning` clock. From 10:00 the default clock airs again,
`daily-summary` comes once at 17:30, and from 19:00 the `night` clock is empty,
so the panel stays with the catcher's idle screen until 07:00. A pitcher that
starts after an extra's time does not air it late; it waits for the next
window. The director logs `show on air` when the show changes.

Validation reports every problem at once: a minute outside `:00`–`:59`, the
same minute twice, a slot without `segment` (also inside a block), an empty
block, a list as `default`, `count` outside 1–10, `spacing` that is no
duration from 1s to 2m, a `card-tags` entry that is not `key=value` or sets
`segment=`, a malformed
window, overlapping windows, a show without a name or with a clock that is not
in `clocks`, an extra without `segment` or with `at` outside its window, or a
brightness outside 0–100. A slot or extra naming a segment that does not
exist (yet), such as `weather` or `daily-summary`, is logged and skipped. It
is not an error, so the schedule can be written ahead of the code.

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
| `time-check` | `casual` (default false) | `It's 15:00` / `Tuesday, 17 November 2026` in `RADIO_TIMEZONE`. With `casual: true` and moderation, the scrolling line is a casual host line that must keep the time and the weekday and add no number (`It's 15:00 on Tuesday, 17 November 2026 — take a quick breather.`); else the date |
| `news` | `count` (1), `feeds` (names, default all), `mix` (default false), `summary` (default false) | the newest headlines of the feeds not aired in the last `RADIO_NEWS_SEEN_FOR`, younger than `RADIO_NEWS_MAX_AGE`, without a word of `RADIO_NEWS_BLOCK_WORDS`. The title is the headline shortened to fit the card; the message is `<feed>: <full headline>`. With `summary: true` and moderation, the message is `<feed>: <one sentence>` summed up from the item's description in the feed (RSS `description`/`content:encoded`, Atom `summary`/`content`): numbers only from the headline and the description; no description, a feed that only gives links and points (the model answers SKIP) or a rejected answer keeps the headline. With `mix: true` the feeds take turns (in the slot's `feeds` order, else `RADIO_NEWS_FEEDS` order) and each feed keeps its own order, so a top-news feed gives its top stories first; a feed with nothing left drops out. Headlines air in the feed's language (shortening keeps it), so a German feed airs German headlines |
| `infra-facts` | `count` (1), `sources` (names, default all), `combine` (default false) | randomly picked facts. The last `RADIO_FACTS_RECENT` picks are avoided while other facts are available. With `combine: true`, a `count` above 1 and moderation, the facts become one sentence on one card (titled with the source, or `Infra facts` for several): it must keep every number of the facts, as often as there, add none, and keep every source name; else they air one by one |
| `daily-summary` | `window` (`hourly`, `daily`, `weekly`; default `daily`), `count` (3), `tags` (names) | the balance of the window from [homerun2-scout](https://github.com/stuttgart-things/homerun2-scout) (`RADIO_SCOUT_URL`), in this order: `24h: 312 msgs, 3 err` / `In the last 24 h: 312 messages, 3 errors, 0 critical (before: 290)`; per tag `24h: 4 release` / `In the last 24 h: 4 messages tagged release`; then scout's top system: `Most alerts: flux` / `In the last 24 h: flux raised the most alerts, 162 of its 162 messages` when it raised any (scout ranks by alerts first and returns only a few systems, so the busiest by messages may be missing), else `Busiest: kubernetes` / `In the last 24 h: kubernetes sent 120 messages`. Scout's windows are rolling (the last 24 h, not since midnight), and the cards say so; when retention has dropped part of the window, "at least" and no comparison. The station's own cards are in scout's index too: set `DIGEST_EXCLUDE_SYSTEMS=radio` (or `RADIO_SYSTEM`) on scout so the counts leave them out; the segment never names its own system |
| `tabletennis-stats` | `days` (1–366, default 7), `count` (3) | the office's table tennis from [schmetterpause](https://github.com/stuttgart-things/schmetterpause)'s `GET /api/statistics` (`RADIO_SCHMETTERPAUSE_URL`, its ADR-0032), confirmed matches only, in this order: `7d: 12 matches` / `Table tennis in the last 7 days: 12 matches, 30 sets, 612 points`; `Longest set 14:12`; `Most wins: Cem` / `… Cem, 5 won and 1 lost` (a tie of two names both, a wider tie nobody); `7d: 4 deuce sets` / `… 4 sets went to deuce and 2 matches ended without a set lost`. When schmetterpause's match cap cut into the window: `7d: 200+ matches` / `… at least 200 matches …`, aired as is (never reworded), and the other cards name the matches they cover (`Most wins in the last 200 matches: …`) instead of the window. Nothing played: nothing airs. Ordinary ambient cards, never a held score, so the panel stays free |
| `did-you-know` | `count` (1), `kinds` (catalog kinds, default `component`, `system`, `api`, `resource`), `types` (`spec.type`, e.g. `service`, `website`; default any) | a random entry of the [Backstage](https://backstage.io) catalog (`RADIO_BACKSTAGE_URL`) whose description says more than its name, never a `deprecated` one (`spec.lifecycle`): `Did you know? homerun2-scout` / `homerun2-scout: Analytics for homerun2 messages`. With moderation, one casual sentence that must keep the entry's name and add no number; else the fixed text. An aired entry is not repeated within `RADIO_DID_YOU_KNOW_SEEN_FOR` |
| `rollouts` | `count` (1), `within` (Go duration, default `6h`), `namespaces` (default all), `kinds` (`deployment`, `statefulset`, `daemonset`, `helmrelease`; default all) | what was deployed lately in the clusters of `sources.yaml`: a `kubernetes` source gives Deployments (newest ReplicaSet), StatefulSets and DaemonSets (newest ControllerRevision) whose current revision is younger than `within`, with the first container's image tag: `Deployed: homerun2-radio-pitcher v0.15.0` / `labul: homerun2-radio-pitcher v0.15.0 rolled out in homerun2 at 14:54` (`deployed` for a first revision); a `flux` source gives HelmReleases whose current Helm revision is that young: `labul: Helm release cert-manager upgraded to chart v1.15.0 in cert-manager at 14:30`. Rollouts of one namespace within 10 minutes share a card: `Deployed: 7 in argocd` / `labul: 7 rollouts in argocd at 12:15: argocd-server v3.6.2, …`. Newest first, each rollout airs once (kept 48 h). Fixed texts, never reworded |
| `releases` | `count` (1), `within` (Go duration, default `24h`), `repos` (names without the org; default all) | the releases `RADIO_RELEASES_ORG` published lately, from the org's public GitHub events (one request, cached 10 min; no drafts, no prereleases), newest first, each once (kept 72 h): `Released: homerun2-radio-pitcher v0.15.0` / `homerun2-radio-pitcher v0.15.0: news mix lets the feeds take turns` (the first entry of the release notes as plain text; without one `stuttgart-things released argocd v0.119.0 at 13:00`). Fixed texts. Releases of other projects (Flux, Kubernetes, …) need no code: put `https://github.com/<owner>/<repo>/releases.atom` into `RADIO_NEWS_FEEDS` |

A segment with nothing to say (every headline seen, every source down) airs
nothing, and the panel keeps its idle clock.

**Host lines.** Any slot can start with an announcement: `intro: true`
(`once news intro=true` and the UI's preview take it too). The card is
`Next up: News` with `The latest headlines`; with moderation the scrolling
line is a casual host line that must name the segment and has no numbers
(`Up next, it's News with the latest headlines for you.`). It airs only
when the segment has something to say, never `Next up` followed by silence,
and takes one more card's time in the slot.

```yaml
clock:
  ":05": { segment: news, count: 2, intro: true }
  ":30": { segment: time-check, casual: true }
```

### Infra facts and sources (`sources.yaml`)

Every number comes from a live query when the segment airs. A failing source
is logged and skipped; the others still report.

```yaml
sources:
  - name: homerun2-dev        # how the source is called on air
    type: kubernetes          # kubernetes | flux | harvester | prometheus
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
| `prometheus` | your `queries` | your templates, see below |
| `harvester` | `vms` | `17 VMs on harvester` / `harvester: 17 VMs, 12 of them running` (`virtualmachines.kubevirt.io/v1`, `status.printableStatus`) |
| | `oldest-vm` | `Oldest VM: db` / `harvester: the oldest VM is db, 400 days old` |

A Flux kind with no objects yields no fact.

#### Prometheus: facts from PromQL

A `prometheus` source has no built-in facts. Each entry of `queries` is one
instant query plus fixed texts, so a new fact is a change to `sources.yaml`,
not to the code. Any metric that Prometheus scrapes can go on air, e.g.
kube-state-metrics, node-exporter, Flux, or clusterscope
(`clusterscope_lab_findings`, `clusterscope_app_gitops_drift`). Range windows
give hourly and daily facts (`[1h]`, `[24h]`, `offset 1d`).

```yaml
sources:
  - name: homerun2-dev
    type: prometheus
    url: http://kube-prometheus-stack-prometheus.monitoring.svc:9090
    # tokenFile: /etc/homerun2-radio-pitcher/kubeconfigs/prometheus-token  # optional bearer token
    # facts: [restarts-1h]   # subset of the query ids; empty = all
    queries:
      - id: restarts-1h
        query: sum(increase(kube_pod_container_status_restarts_total[1h]))
        title: "Restarts: {{.Value}}"
        message: "{{.Source}}: {{.Value}} pod restarts in the last hour"
      - id: alerts-firing
        query: count(ALERTS{alertstate="firing"}) > 0   # empty result: nothing to say
        message: "{{.Source}}: {{.Value}} alerts firing right now"
      - id: busy-node
        query: topk(1, 100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m]))))
        decimals: 1
        message: "{{.Source}}: busiest node {{.Labels.instance}} at {{.Value}}% CPU"
```

| Key | | |
|---|---|---|
| `id` | required | unique within the source; with the sample's labels it identifies the fact (repeat avoidance) |
| `query` | required | PromQL, evaluated when the segment airs; vector or scalar |
| `message` | required | scrolling line, Go `text/template` |
| `title` | `message` | card title, fitted to the card |
| `decimals` | `0` | rounding of `.Value` |
| `limit` | `10` | facts per query at most (samples in label order) |

Templates see `.Source` (the source name), `.Value` (the number, rounded),
`.Float` (the raw value, for `{{if gt .Float 10.0}}`) and `.Labels` (the
sample's labels; a missing label is empty). Every sample of a vector is one
fact; an empty result, `NaN` and `Inf` say nothing, so filter in PromQL
(`> 0`). The numbers come only from the query: the template holds the
wording, the moderator may reword it but never change a number. A failing
query is logged and skipped while the other queries still report; when every
query fails, the source counts as failed.

#### RBAC for the kubeconfigs

Only `list` is needed, cluster-wide, and nothing else. No `get`, no Secrets,
no write.

| Type | apiGroups | resources | verbs |
|---|---|---|---|
| `kubernetes` | `""` | `pods`, `nodes`, `namespaces` (only those in `facts`) | `list` |
| `kubernetes` (rollouts) | `apps` | `replicasets`, `controllerrevisions` | `list` |
| `flux` | `kustomize.toolkit.fluxcd.io`, `helm.toolkit.fluxcd.io` | `kustomizations`, `helmreleases` | `list` |
| `harvester` | `kubevirt.io` | `virtualmachines` | `list` |

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: homerun2-radio-reader
rules:
  - { apiGroups: [""], resources: [pods, nodes, namespaces], verbs: [list] }
  - { apiGroups: [apps], resources: [replicasets, controllerrevisions], verbs: [list] }
  - { apiGroups: [kustomize.toolkit.fluxcd.io], resources: [kustomizations], verbs: [list] }
  - { apiGroups: [helm.toolkit.fluxcd.io], resources: [helmreleases], verbs: [list] }
  - { apiGroups: [kubevirt.io], resources: [virtualmachines], verbs: [list] }
```

Bind it to the identity in each kubeconfig, for example a ServiceAccount
token. ReplicaSets and ControllerRevisions carry the pod template, so the
reader sees literal `env` values of workloads (not Secrets); the radio only
reads the image. Without that rule the `rollouts` segment reports the
forbidden list and stays silent for that source. For sources without a kubeconfig (in-cluster), the KCL option
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
  invented number), or uses one more often than the input does
- spells a number out that the input does not (`sixty-four`, `nine`,
  `dozen`, German `zwölf`, `vierundsechzig`): numbers stay digits
- drops the source name of an infra fact (e.g. `homerun2-dev`)
- is empty, or is too long for the card or for `RADIO_MESSAGE_MAX`
- times out or errors

Only the first line of an answer counts; quotes and markdown are stripped.
Use the local Ollama in the lab for the station. Where it is not reachable
(e.g. testing from a laptop), any OpenAI-compatible endpoint works with a key,
for example OpenRouter's free models:

```bash
RADIO_LLM_ENABLED=true \
OLLAMA_URL=https://openrouter.ai/api \
OLLAMA_MODEL=google/gemma-3-12b-it:free \
LLM_API_KEY=sk-or-... \
go run . once news -dry-run
```

The free tier is rate-limited (`429 free-models-per-day`); a rejected or
failed call airs the fixed text.

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
| `RADIO_ARCHIVE_SIZE` | `300` | how many aired cards the UI's archive keeps (in memory) |
| `RADIO_TITLE_LINES` / `RADIO_TITLE_WIDTH` | `4` / `16` | title space; `3` / `12` keeps titles in the catcher's larger 5x7 font |
| `RADIO_MESSAGE_MAX` | `120` | scrolling line, characters |
| `RADIO_NEWS_FEEDS` | | `name=url,name=url` (RSS 2.0 or Atom); an entry without `name=` is named after its host |
| `RADIO_NEWS_TIMEOUT` | `10s` | per feed |
| `RADIO_NEWS_SEEN_FOR` | `12h` | an aired headline is not repeated within this |
| `RADIO_NEWS_MAX_AGE` | `48h` | older headlines are skipped (undated ones are kept) |
| `RADIO_NEWS_BLOCK_WORDS` | a short list of English swear words | comma-separated; a headline with one of them is skipped. Matched at the start of a word, case-insensitive (`fuck` also catches `fucking`, `cunt` not `Scunthorpe`). Replaces the default; `none` blocks nothing |
| `RADIO_SOURCE_TIMEOUT` | `10s` | per source query |
| `RADIO_FACTS_RECENT` | `4` | recently aired facts avoided |
| `RADIO_SCOUT_URL` | | homerun2-scout for `daily-summary`, e.g. `http://homerun2-scout.homerun2.svc`; unset: daily-summary stays silent |
| `RADIO_SCOUT_TOKEN` | | scout's `AUTH_TOKEN` (bearer) |
| `RADIO_SCHMETTERPAUSE_URL` | | schmetterpause for `tabletennis-stats`, e.g. `http://schmetterpause.schmetterpause.svc`; unset: tabletennis-stats stays silent |
| `RADIO_BACKSTAGE_URL` | | Backstage backend for `did-you-know`, e.g. `https://backstage.example.com`; unset: did-you-know stays silent |
| `RADIO_BACKSTAGE_TOKEN` | | a static external access token of Backstage (`backend.auth.externalAccess`, restricted to the `catalog` plugin) |
| `RADIO_RELEASES_ORG` | | GitHub organization for `releases`; empty: releases slots stay silent |
| `RADIO_GITHUB_TOKEN` | | optional GitHub token (public read is enough). Without it GitHub allows 60 requests an hour; `releases` makes one per 10 minutes at most |
| `RADIO_DID_YOU_KNOW_SEEN_FOR` | `168h` | an aired catalog entry is not repeated within this |
| `RADIO_SCHMETTERPAUSE_TOKEN` | | schmetterpause's `SP_STATISTICS_TOKEN` (bearer; a reader's token, it cannot enter results) |
| `RADIO_LLM_ENABLED` | `false` | moderation switch |
| `OLLAMA_URL` | | e.g. `http://ollama.ollama.svc.cluster.local:11434` |
| `OLLAMA_MODEL` | | e.g. `llama3.2:3b` |
| `OLLAMA_API` | `openai` | `openai` (`/v1/chat/completions`) or `native` (`/api/chat`) |
| `LLM_API_KEY` | | optional, sent as `Authorization: Bearer` (OpenRouter and other OpenAI-compatible endpoints) |
| `OLLAMA_TIMEOUT` | `20s` | per call |
| `RADIO_DRY_RUN` | `false` | print messages as JSON lines instead of pitching |
| `PORT` | `8080` | |
| `BASE_PATH` | | prefix of the programme UI, e.g. `/radio` behind homerun2-portal |
| `RADIO_UI_ENABLED` | `true` | serve the programme UI |
| `RADIO_UI_TOKEN` | | at least 16 characters; enables the session controls of the UI (below); unset: the UI is read-only |
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
| `GET /metrics` | Prometheus; `homerun2_radio_items_total{segment,result}`; with moderation `homerun2_radio_moderation_total{kind,result}`: `kind` headline, summary, fact, facts (combined), blurb, intro, time; `result` `used` (the model's answer aired) or why the fixed text aired: `rejected_number`, `rejected_name`, `rejected_length`, `rejected_empty`, `rejected_echo` (only repeated the input), `error` (timeout, HTTP error, rate limit). UI previews count too |
| `GET <BASE_PATH>/` | programme UI (below) |
| `GET <BASE_PATH>/ui/manifest.json` | fragments for homerun2-portal: `on-air`, `last-aired` |

`/health`, `/ready` and `/metrics` stay at the root whatever `BASE_PATH` is.

### Programme UI

A read-only page (htmx) for the people next to the panel:

- **On air**: the show and its clock, what airs next and when, the day as a
  timeline with the current position, the extras of the show (pending,
  done, or not built yet)
- **Clock**: the slots of the clock on air, the next one highlighted;
  segments that do not exist yet are marked as skipped
- **Last aired**: the last 20 cards with their time and segment (since the
  start of the pod); a card with a link (a news article) has `↗`
- **Archive** (`<BASE_PATH>/aired`, linked from Last aired): up to
  `RADIO_ARCHIVE_SIZE` cards since the start of the pod, newest first,
  filtered by segment and a search text over title and scrolling line, with
  the article links. In memory: a restart empties it
- **Sources**: each source's last query: when, how long, how many facts, or
  the error. Sources are queried when infra-facts airs, not by the page.
- **Preview**: builds a segment now with parameters (`count=2 feeds=cncf`),
  as the next slot would air it, without pitching it. The cards are wrapped
  like the matrix wraps them; a title that does not fit is red. A preview
  marks no headline as seen and no fact as recent, so it does not change
  what airs; it does query the feeds, the sources and the model (one
  preview at a time).

Behind [homerun2-portal](https://github.com/stuttgart-things/homerun2-portal)
the pitcher runs with `BASE_PATH=/radio`; the portal proxies `/radio/*` to
it, shows the page under `/ui/radio`, puts the `on-air` and `last-aired`
fragments on its overview, and its theme reaches the page through
`X-Forwarded-Prefix`. Without the portal the page works on its own at
`http://<pod>:8080/radio/`.

### Session controls

With `RADIO_UI_TOKEN` set, the page gets a **Control** section. The schedule
in git stays the programme; the controls only lay short-lived overrides on
top of it, for the running pod:

| Control | Effect |
|---|---|
| Pause (15 min … 12 h) | nothing airs until then; "Resume now" ends it |
| Skip | the next slot that would air is skipped once |
| Switch the clock (15 min … 12 h) | `default` or a clock from `clocks:` replaces the show's clock, e.g. `night` for silence; extras still air |
| Air now | a segment with parameters airs at once (unless a slot is on air), also while paused |

Overrides end on their own (at most 24 h), survive a schedule reload, and are
lost on a restart. "On air" and "next" show them, and every change is logged
(`override: ...`).

The token is entered once in the page ("Unlock"); the page then holds an
`HttpOnly`, `SameSite=Strict` cookie (12 h, a hash of the token, not the
token). A change through the cookie also needs htmx's `HX-Request` header,
which a form on another site cannot send. Scripts use the token directly:

```bash
curl -X POST -H "Authorization: Bearer $RADIO_UI_TOKEN" -d minutes=60 \
  https://portal.example/radio/ui/control/pause
curl -X POST -H "Authorization: Bearer $RADIO_UI_TOKEN" -d segment=news -d params=count=1 \
  https://portal.example/radio/ui/control/air
```

Actions: `pause` (`minutes`), `resume`, `skip`, `unskip`, `clock` (`clock`,
`minutes`), `restore`, `air` (`segment`, `params`).

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
