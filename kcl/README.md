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
| `config.redisPasswordSecretName`, `config.redisPasswordSecretKey` | , `password` | existing Secret with the Redis password |
| `config.redisPassword` | | renders Secret `<name>-redis` when no existing Secret is named |
| `config.scheduleYaml` | **required** | `schedule.yaml` |
| `config.sourcesYaml` | | `sources.yaml`; empty: no infra-facts |
| `config.kubeconfigSecret` | | existing Secret with kubeconfig files, mounted at `/etc/homerun2-radio-pitcher/kubeconfigs/` |
| `config.inClusterReadAccess` | `false` | ClusterRole + binding: `list` on pods, nodes, namespaces, kustomizations, helmreleases, virtualmachines |
| `config.timezone` | `Europe/Berlin` | |
| `config.newsFeeds` | | `name=url,name=url` |
| `config.llmEnabled`, `config.ollamaUrl`, `config.ollamaModel`, `config.ollamaApi` | off, , , `openai` | |
| `config.llmApiKeySecretName`, `config.llmApiKeySecretKey` | , `api-key` | existing Secret with an API key (e.g. OpenRouter), as `LLM_API_KEY`; required ref |
| `config.servicePort` | `80` | |
| `config.extraEnvVars` | `{}` | any further `RADIO_*` variable |
| `config.labels` | `{}` | |
