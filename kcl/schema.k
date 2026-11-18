"""
Schema for deploying homerun2-radio-pitcher.
"""

schema RadioPitcher:
    """Configuration for a homerun2-radio-pitcher deployment"""

    name: str = "homerun2-radio-pitcher"
    namespace: str = "homerun2"

    image: str = "ghcr.io/stuttgart-things/homerun2-radio-pitcher:main"
    imagePullPolicy: str = "IfNotPresent"

    # One director per stream: two replicas would air every slot twice.
    replicas: int = 1
    cpuRequest: str = "20m"
    cpuLimit: str = "200m"
    memoryRequest: str = "32Mi"
    memoryLimit: str = "128Mi"

    # Redis of homerun2. The password comes from a Secret: either
    # <name>-redis (key password), rendered when redisPassword is given, or an
    # existing one named by redisPasswordSecretName / redisPasswordSecretKey.
    redisAddr: str = "redis-stack.homerun2.svc.cluster.local"
    redisPort: str = "6379"
    redisStream: str = "ambient"
    redisPassword: str = ""
    redisPasswordSecretName: str = ""
    redisPasswordSecretKey: str = "password"

    # schedule.yaml (ConfigMap <name>-config, key schedule.yaml)
    scheduleYaml: str
    # sources.yaml (same ConfigMap); empty: no infra-facts
    sourcesYaml: str = ""
    # Secret with kubeconfig files, mounted at /etc/homerun2-radio-pitcher/kubeconfigs;
    # sources.yaml refers to them by path. Created outside (SOPS, ESO).
    kubeconfigSecret: str = ""

    timezone: str = "Europe/Berlin"
    # Programme UI: BASE_PATH behind homerun2-portal (e.g. "/radio"); off
    # with uiEnabled = False
    basePath: str = ""
    uiEnabled: bool = True
    # Session controls of the UI: RADIO_UI_TOKEN from an existing Secret
    uiTokenSecretName: str = ""
    uiTokenSecretKey: str = "ui-token"
    # "name=url,name=url"
    newsFeeds: str = ""
    # homerun2-scout for daily-summary; the token from an existing Secret
    scoutUrl: str = ""
    scoutTokenSecretName: str = ""
    scoutTokenSecretKey: str = "token"

    # Moderation through a local Ollama, off by default
    llmEnabled: bool = False
    ollamaUrl: str = ""
    ollamaModel: str = ""
    ollamaApi: "openai" | "native" = "openai"
    # Optional API key (e.g. OpenRouter) from an existing Secret, as LLM_API_KEY
    llmApiKeySecretName: str = ""
    llmApiKeySecretKey: str = "api-key"

    # ClusterRole for sources that use the in-cluster ServiceAccount (an
    # empty kubeconfig in sources.yaml): list pods, nodes, namespaces, Flux
    # Kustomizations and HelmReleases, and KubeVirt VirtualMachines.
    inClusterReadAccess: bool = False

    servicePort: int = 80

    extraEnvVars: {str:str} = {}
    labels: {str:str} = {}

    check:
        name != "", "name must not be empty"
        namespace != "", "namespace must not be empty"
        replicas in [0, 1], "replicas must be 0 or 1: a second director airs every slot twice"
        scheduleYaml != "", "scheduleYaml is required"
        not llmEnabled or (ollamaUrl != "" and ollamaModel != ""), "llmEnabled needs ollamaUrl and ollamaModel"
