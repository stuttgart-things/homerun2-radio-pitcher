"""
Deployment: one director that airs the schedule.
"""

import labels

config = labels.config

_configDir = "/etc/homerun2-radio-pitcher"
_kubeconfigDir = "/etc/homerun2-radio-pitcher/kubeconfigs"

_redisSecretName = config.redisPasswordSecretName or "{}-redis".format(config.name)
# The password Secret is either named (existing) or rendered from redisPassword.
# When there is one, the ref is required: a wrong name or key keeps the pod in
# CreateContainerConfigError with a clear event instead of starting without a
# password and failing every pitch with NOAUTH. No Secret: no REDIS_PASSWORD.
_redisSecretKey = config.redisPasswordSecretKey if config.redisPasswordSecretName else "password"
_hasRedisSecret = bool(config.redisPasswordSecretName or config.redisPassword)

_env = [
    {name: "LOG_FORMAT", value: "json"}
    {name: "REDIS_ADDR", value: config.redisAddr}
    {name: "REDIS_PORT", value: config.redisPort}
    {name: "REDIS_STREAM", value: config.redisStream}
] + ([
    {
        name: "REDIS_PASSWORD"
        valueFrom: {
            secretKeyRef: {
                name: _redisSecretName
                key: _redisSecretKey
                optional: False
            }
        }
    }
] if _hasRedisSecret else []) + [
    {name: "RADIO_SCHEDULE_PATH", value: "{}/schedule.yaml".format(_configDir)}
    {name: "RADIO_TIMEZONE", value: config.timezone}
] + ([
    {name: "RADIO_SOURCES_PATH", value: "{}/sources.yaml".format(_configDir)}
] if config.sourcesYaml else []) + ([
    {name: "RADIO_NEWS_FEEDS", value: config.newsFeeds}
] if config.newsFeeds else []) + ([
    {name: "RADIO_LLM_ENABLED", value: "true"}
    {name: "OLLAMA_URL", value: config.ollamaUrl}
    {name: "OLLAMA_MODEL", value: config.ollamaModel}
    {name: "OLLAMA_API", value: config.ollamaApi}
] if config.llmEnabled else []) + ([
    {
        name: "LLM_API_KEY"
        valueFrom: {
            secretKeyRef: {
                name: config.llmApiKeySecretName
                key: config.llmApiKeySecretKey
                optional: False
            }
        }
    }
] if config.llmEnabled and config.llmApiKeySecretName else []) + [
    {name: k, value: v} for k, v in config.extraEnvVars
]

deployment = {
    apiVersion: "apps/v1"
    kind: "Deployment"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    spec: {
        replicas: config.replicas
        # never two directors at once, not even during a rollout
        strategy: {type: "Recreate"}
        selector: {matchLabels: labels.selectorLabels}
        template: {
            metadata: {labels: labels.commonLabels}
            spec: {
                serviceAccountName: config.name
                containers: [
                    {
                        name: config.name
                        image: config.image
                        imagePullPolicy: config.imagePullPolicy
                        args: ["serve"]
                        env: _env
                        ports: [{name: "http", containerPort: 8080, protocol: "TCP"}]
                        readinessProbe: {
                            httpGet: {path: "/ready", port: "http"}
                            periodSeconds: 10
                        }
                        livenessProbe: {
                            httpGet: {path: "/health", port: "http"}
                            periodSeconds: 20
                        }
                        resources: {
                            requests: {cpu: config.cpuRequest, memory: config.memoryRequest}
                            limits: {cpu: config.cpuLimit, memory: config.memoryLimit}
                        }
                        securityContext: {
                            readOnlyRootFilesystem: True
                            runAsNonRoot: True
                            allowPrivilegeEscalation: False
                            capabilities: {drop: ["ALL"]}
                        }
                        volumeMounts: [
                            {name: "config", mountPath: _configDir, readOnly: True}
                        ] + ([
                            {name: "kubeconfigs", mountPath: _kubeconfigDir, readOnly: True}
                        ] if config.kubeconfigSecret else [])
                    }
                ]
                securityContext: {
                    runAsNonRoot: True
                    runAsUser: 65532
                    runAsGroup: 65532
                    fsGroup: 65532
                    seccompProfile: {type: "RuntimeDefault"}
                }
                volumes: [
                    {name: "config", configMap: {name: "{}-config".format(config.name)}}
                ] + ([
                    {name: "kubeconfigs", secret: {secretName: config.kubeconfigSecret}}
                ] if config.kubeconfigSecret else [])
            }
        }
    }
}
