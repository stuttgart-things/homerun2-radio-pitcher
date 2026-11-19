"""
ConfigMap with schedule.yaml, sources.yaml and spots.yaml. The pitcher re-reads the
schedule when Kubernetes updates the mounted file: a schedule change needs
no restart.
"""

import labels

config = labels.config

configMap = {
    apiVersion: "v1"
    kind: "ConfigMap"
    metadata: {
        name: "{}-config".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    data: {
        "schedule.yaml": config.scheduleYaml
        if config.sourcesYaml:
            "sources.yaml": config.sourcesYaml
        if config.spotsYaml:
            "spots.yaml": config.spotsYaml
    }
}
