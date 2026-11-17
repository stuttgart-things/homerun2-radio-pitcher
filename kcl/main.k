"""
homerun2-radio-pitcher. Namespaces are not emitted: the consuming
Kustomization or Application creates them, like for the sibling services.
"""

import serviceaccount
import configmap
import secret
import rbac
import deploy
import service

manifests = [
    m for m in [
        serviceaccount.serviceAccount
        configmap.configMap
        secret.secretRedis
        rbac.clusterRole
        rbac.clusterRoleBinding
        deploy.deployment
        service.service
    ] if m
]
