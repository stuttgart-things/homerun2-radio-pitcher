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
    newsFeeds = option("config.newsFeeds")
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
    if _opts.newsFeeds:
        newsFeeds = _opts.newsFeeds
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
