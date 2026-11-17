"""
Read access for in-cluster sources (inClusterReadAccess): list only, no
get on Secrets or anything else.
"""

import labels

config = labels.config

_roleName = "{}-read".format(config.name)

clusterRole = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "ClusterRole"
    metadata: {
        name: _roleName
        labels: labels.commonLabels
    }
    rules: [
        {apiGroups: [""], resources: ["pods", "nodes", "namespaces"], verbs: ["list"]}
        {apiGroups: ["kustomize.toolkit.fluxcd.io"], resources: ["kustomizations"], verbs: ["list"]}
        {apiGroups: ["helm.toolkit.fluxcd.io"], resources: ["helmreleases"], verbs: ["list"]}
        {apiGroups: ["kubevirt.io"], resources: ["virtualmachines"], verbs: ["list"]}
    ]
} if config.inClusterReadAccess else None

clusterRoleBinding = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "ClusterRoleBinding"
    metadata: {
        name: _roleName
        labels: labels.commonLabels
    }
    roleRef: {
        apiGroup: "rbac.authorization.k8s.io"
        kind: "ClusterRole"
        name: _roleName
    }
    subjects: [
        {kind: "ServiceAccount", name: config.name, namespace: config.namespace}
    ]
} if config.inClusterReadAccess else None
