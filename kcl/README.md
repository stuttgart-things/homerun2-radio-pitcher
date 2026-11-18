# KCL: homerun2-radio-pitcher

Renders a Deployment (one replica, `Recreate`), the ConfigMap with
`schedule.yaml` and `sources.yaml`, a Service for `/health`, `/ready` and
`/metrics`, and optionally a Redis password Secret and a read-only
ClusterRole for in-cluster sources.

```bash
dagger call -m github.com/stuttgart-things/dagger/kcl@v0.82.0 run \
  --source kcl --parameters-file tests/kcl-deploy-profile.yaml \
  export --path /tmp/radio.yaml

kcl run kcl -D config.scheduleYaml="$(cat examples/schedule.yaml)"
```

| Option | Default | |
|---|---|---|
| `config.name`, `config.namespace` | `homerun2-radio-pitcher`, `homerun2` | |
| `config.image` | `ghcr.io/stuttgart-things/homerun2-radio-pitcher:main` | the release substitutes the tag |
| `config.replicas` | `1` | 0 or 1 only |
| `config.redisAddr`, `config.redisPort`, `config.redisStream` | `redis-stack.homerun2.svc.cluster.local`, `6379`, `ambient` | |
| `config.redisPasswordSecretName`, `config.redisPasswordSecretKey` | , `password` | existing Secret with the Redis password; the ref is required, so a wrong name or key stops the pod (the homerun2 stack's `redis-stack-auth` uses `redis-password`) |
| `config.redisPassword` | | renders Secret `<name>-redis` when no existing Secret is named; with neither, `REDIS_PASSWORD` is not set |
| `config.scheduleYaml` | **required** | `schedule.yaml` |
| `config.sourcesYaml` | | `sources.yaml`; empty: no infra-facts |
| `config.kubeconfigSecret` | | existing Secret with kubeconfig files, mounted at `/etc/homerun2-radio-pitcher/kubeconfigs/` |
| `config.inClusterReadAccess` | `false` | ClusterRole + binding: `list` on pods, nodes, namespaces, kustomizations, helmreleases, virtualmachines |
| `config.timezone` | `Europe/Berlin` | |
| `config.basePath` | | `BASE_PATH` of the programme UI, e.g. `/radio` behind homerun2-portal |
| `config.uiEnabled` | `true` | read-only programme UI |
| `config.uiTokenSecretName`, `config.uiTokenSecretKey` | , `ui-token` | existing Secret with `RADIO_UI_TOKEN`: enables the session controls; required ref |
| `config.newsFeeds` | | `name=url,name=url` |
| `config.scoutUrl` | | homerun2-scout for `daily-summary`, e.g. `http://homerun2-scout` |
| `config.scoutTokenSecretName`, `config.scoutTokenSecretKey` | , `token` | existing Secret with scout's `AUTH_TOKEN`, as `RADIO_SCOUT_TOKEN`; required ref |
| `config.backstageUrl` | | Backstage backend for `did-you-know` |
| `config.backstageTokenSecretName`, `config.backstageTokenSecretKey` | , `backstage-token` | existing Secret with a Backstage static external access token, as `RADIO_BACKSTAGE_TOKEN`; required ref |
| `config.schmetterpauseUrl` | | schmetterpause for `tabletennis-stats`, e.g. `http://schmetterpause.schmetterpause.svc` |
| `config.schmetterpauseTokenSecretName`, `config.schmetterpauseTokenSecretKey` | , `statistics-token` | existing Secret with schmetterpause's `SP_STATISTICS_TOKEN`, as `RADIO_SCHMETTERPAUSE_TOKEN`; required ref |
| `config.llmEnabled`, `config.ollamaUrl`, `config.ollamaModel`, `config.ollamaApi` | off, , , `openai` | |
| `config.llmApiKeySecretName`, `config.llmApiKeySecretKey` | , `api-key` | existing Secret with an API key (e.g. OpenRouter), as `LLM_API_KEY`; required ref |
| `config.servicePort` | `80` | |
| `config.extraEnvVars` | `{}` | any further `RADIO_*` variable |
| `config.labels` | `{}` | |
