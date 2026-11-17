"""
Redis password Secret, rendered only when a value is given. In GitOps the
password usually comes from an existing Secret (redisPasswordSecretName).
"""

import base64
import labels

config = labels.config

secretRedis = {
    apiVersion: "v1"
    kind: "Secret"
    metadata: {
        name: "{}-redis".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    type: "Opaque"
    data: {
        password: base64.encode(config.redisPassword)
    }
} if config.redisPassword and not config.redisPasswordSecretName else None
