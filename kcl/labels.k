"""
Configuration from options and common labels
"""

import schema

_opts = {
    name = option("config.name")
    namespace = option("config.namespace")
    image = option("config.image")
    imagePullPolicy = option("config.imagePullPolicy")
    replicas = option("config.replicas")
    redisAddr = option("config.redisAddr")
    redisPort = option("config.redisPort")
    redisStream = option("config.redisStream")
    redisPassword = option("config.redisPassword")
    redisPasswordSecretName = option("config.redisPasswordSecretName")
    redisPasswordSecretKey = option("config.redisPasswordSecretKey")
    scheduleYaml = option("config.scheduleYaml")
    sourcesYaml = option("config.sourcesYaml")
    kubeconfigSecret = option("config.kubeconfigSecret")
    timezone = option("config.timezone")
    basePath = option("config.basePath")
    uiEnabled = option("config.uiEnabled")
    uiTokenSecretName = option("config.uiTokenSecretName")
    uiTokenSecretKey = option("config.uiTokenSecretKey")
    newsFeeds = option("config.newsFeeds")
    scoutUrl = option("config.scoutUrl")
    scoutTokenSecretName = option("config.scoutTokenSecretName")
    scoutTokenSecretKey = option("config.scoutTokenSecretKey")
    schmetterpauseUrl = option("config.schmetterpauseUrl")
    schmetterpauseTokenSecretName = option("config.schmetterpauseTokenSecretName")
    schmetterpauseTokenSecretKey = option("config.schmetterpauseTokenSecretKey")
    backstageUrl = option("config.backstageUrl")
    backstageTokenSecretName = option("config.backstageTokenSecretName")
    backstageTokenSecretKey = option("config.backstageTokenSecretKey")
    llmEnabled = option("config.llmEnabled")
    ollamaUrl = option("config.ollamaUrl")
    ollamaModel = option("config.ollamaModel")
    ollamaApi = option("config.ollamaApi")
    llmApiKeySecretName = option("config.llmApiKeySecretName")
    llmApiKeySecretKey = option("config.llmApiKeySecretKey")
    inClusterReadAccess = option("config.inClusterReadAccess")
    servicePort = option("config.servicePort")
    extraEnvVars = option("config.extraEnvVars")
    labels = option("config.labels")
}

_true = lambda v -> bool {
    v in [True, "True", "true"]
}

config: schema.RadioPitcher = schema.RadioPitcher {
    if _opts.name:
        name = _opts.name
    if _opts.namespace:
        namespace = _opts.namespace
    if _opts.image:
        image = _opts.image
    if _opts.imagePullPolicy:
        imagePullPolicy = _opts.imagePullPolicy
    if _opts.replicas != None:
        replicas = int(_opts.replicas)
    if _opts.redisAddr:
        redisAddr = _opts.redisAddr
    if _opts.redisPort:
        redisPort = str(_opts.redisPort)
    if _opts.redisStream:
        redisStream = _opts.redisStream
    if _opts.redisPassword:
        redisPassword = _opts.redisPassword
    if _opts.redisPasswordSecretName:
        redisPasswordSecretName = _opts.redisPasswordSecretName
    if _opts.redisPasswordSecretKey:
        redisPasswordSecretKey = _opts.redisPasswordSecretKey
    scheduleYaml = _opts.scheduleYaml or ""
    if _opts.sourcesYaml:
        sourcesYaml = _opts.sourcesYaml
    if _opts.kubeconfigSecret:
        kubeconfigSecret = _opts.kubeconfigSecret
    if _opts.timezone:
        timezone = _opts.timezone
    if _opts.basePath:
        basePath = _opts.basePath
    if _opts.uiEnabled != None:
        uiEnabled = _true(_opts.uiEnabled)
    if _opts.uiTokenSecretName:
        uiTokenSecretName = _opts.uiTokenSecretName
    if _opts.uiTokenSecretKey:
        uiTokenSecretKey = _opts.uiTokenSecretKey
    if _opts.newsFeeds:
        newsFeeds = _opts.newsFeeds
    if _opts.scoutUrl:
        scoutUrl = _opts.scoutUrl
    if _opts.scoutTokenSecretName:
        scoutTokenSecretName = _opts.scoutTokenSecretName
    if _opts.scoutTokenSecretKey:
        scoutTokenSecretKey = _opts.scoutTokenSecretKey
    if _opts.schmetterpauseUrl:
        schmetterpauseUrl = _opts.schmetterpauseUrl
    if _opts.schmetterpauseTokenSecretName:
        schmetterpauseTokenSecretName = _opts.schmetterpauseTokenSecretName
    if _opts.schmetterpauseTokenSecretKey:
        schmetterpauseTokenSecretKey = _opts.schmetterpauseTokenSecretKey
    if _opts.backstageUrl:
        backstageUrl = _opts.backstageUrl
    if _opts.backstageTokenSecretName:
        backstageTokenSecretName = _opts.backstageTokenSecretName
    if _opts.backstageTokenSecretKey:
        backstageTokenSecretKey = _opts.backstageTokenSecretKey
    if _opts.llmEnabled:
        llmEnabled = _true(_opts.llmEnabled)
    if _opts.ollamaUrl:
        ollamaUrl = _opts.ollamaUrl
    if _opts.ollamaModel:
        ollamaModel = _opts.ollamaModel
    if _opts.ollamaApi:
        ollamaApi = _opts.ollamaApi
    if _opts.llmApiKeySecretName:
        llmApiKeySecretName = _opts.llmApiKeySecretName
    if _opts.llmApiKeySecretKey:
        llmApiKeySecretKey = _opts.llmApiKeySecretKey
    if _opts.inClusterReadAccess:
        inClusterReadAccess = _true(_opts.inClusterReadAccess)
    if _opts.servicePort:
        servicePort = int(_opts.servicePort)
    if _opts.extraEnvVars:
        extraEnvVars = _opts.extraEnvVars
    if _opts.labels:
        labels = _opts.labels
}

commonLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
    "app.kubernetes.io/component": "radio"
    "app.kubernetes.io/part-of": "homerun2"
    **config.labels
}

selectorLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
}
