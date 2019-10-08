# Vault Secrets webhook

This chart will install a mutating admission webhook, that injects an executable to containers in a deployment/statefulset which than can request secrets from Vault through environment variable definitions.

## Before you start

Before you install this chart you must create a namespace for it, this is due to the order in which the resources in the charts are applied (Helm collects all of the resources in a given Chart and it's dependencies, groups them by resource type, and then installs them in a predefined order (see [here](https://github.com/helm/helm/blob/release-2.10/pkg/tiller/kind_sorter.go#L29) - Helm 2.10).

The `MutatingWebhookConfiguration` gets created before the actual backend Pod which serves as the webhook itself, Kubernetes would like to mutate that pod as well, but it is not ready to mutate yet (infinite recursion in logic).

## Using External Vault Instances

You will need to add the following annotations to the resources that you wish to mutate:

```
vault.security.banzaicloud.io/vault-addr: https://[URL FOR VAULT]
vault.security.banzaicloud.io/vault-path: [Auth Pass]
vault.security.banzaicloud.io/vault-role: [Auth role]
vault.security.banzaicloud.io/vault-skip-verify: "true" # Container is missing Trusted Mozilla roots too.
```

Be mindful about how you reference Vault secrets itself. For KV v2 secrets, you will need to add the /data/ to the path of the secret.

```
PS C:\> vault kv get kv/rax/test
====== Metadata ======
Key              Value
---              -----
created_time     2019-09-21T16:55:26.479739656Z
deletion_time    n/a
destroyed        false
version          1

=========== Data ===========
Key                    Value
---                    -----
MYSQL_PASSWORD         3xtr3ms3cr3t
MYSQL_ROOT_PASSWORD    s3cr3t
```

The secret shown above is referenced like this:

```
vault:[ENGINE]/data/[SECRET_NAME]#KEY
vault:kv/rax/data/test#MYSQL_PASSWORD
```

If you want to use a specific key version, you can append it after the key so it becomes like this:

`vault:kv/rax/data/test#MYSQL_PASSWORD#1`

Omitting the version will tell Vault to pull the latest version.

Also, you will need to create the following RBAC policy to handle the Vault Auth tokens.

```
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  name: vault-tokenreview-binding
  namespace: vswh # Change it if you don't use the defaults when you install it via Helm
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
- kind: ServiceAccount
  name: vswh-vault-secrets-webhook # Change it if you don't use the defaults when you install it via Helm
  namespace: vswh # Change it if you don't use the defaults when you install it via Helm
```

### Creating the namespace

The namespace must have a label of `name` with the namespace name as it's value.

set the target namespace name or skip for the default name: vswh

```bash
export WEBHOOK_NS=`<namespace>`
```

```bash
WEBHOOK_NS=${WEBHOOK_NS:-vswh}
echo kubectl create namespace "${WEBHOOK_NS}"
echo kubectl label ns "${WEBHOOK_NS}" name="${WEBHOOK_NS}"
```

## Installing the Chart

```bash
$ helm repo add banzaicloud-stable http://kubernetes-charts.banzaicloud.com/branch/master
$ helm repo update
```

```bash
$ helm upgrade --namespace vswh --install vswh banzaicloud-stable/vault-secrets-webhook --wait
```

**NOTE**: `--wait` is necessary because of Helm timing issues, please see [this issue](https://github.com/banzaicloud/banzai-charts/issues/888).

### About GKE Private Clusters

When Google configure the control plane for private clusters, they automatically configure VPC peering between your Kubernetes cluster’s network in a separate Google managed project.

The auto-generated rules **only** open ports 10250 and 443 between masters and nodes. This means that in order to use the webhook component with a GKE private cluster, you must configure an additional firewall rule to allow your masters CIDR to access your webhook pod using the port 8443.

You can read more information on how to add firewall rules for the GKE control plane nodes in the [GKE docs](https://cloud.google.com/kubernetes-engine/docs/how-to/private-clusters#add_firewall_rules).

## Configuration

The following tables lists configurable parameters of the vault-secrets-webhook chart and their default values:

| Parameter                                | Description                                                                  | Default                             |
| ---------------------------------------- | ---------------------------------------------------------------------------- | ----------------------------------- |
| affinity                                 | affinities to use                                                            | `{}`                                |
| debug                                    | debug logs for webhook                                                       | `false`                             |
| image.pullPolicy                         | image pull policy                                                            | `IfNotPresent`                      |
| image.repository                         | image repo that contains the admission server                                | `banzaicloud/vault-secrets-webhook` |
| image.tag                                | image tag                                                                    | `0.5.1`                             |
| image.imagePullSecrets                   | image pull secrets for private repositories                                  | `[]`                                |
| namespaceSelector                        | namespace selector to use, will limit webhook scope                          | `{}`                                |
| nodeSelector                             | node selector to use                                                         | `{}`                                |
| podAnnotations                           | extra annotations to add to pod metadata                                     | `{}`                                |
| replicaCount                             | number of replicas                                                           | `2`                                 |
| resources                                | resources to request                                                         | `{}`                                |
| service.externalPort                     | webhook service external port                                                | `443`                               |
| service.name                             | webhook service name                                                         | `vault-secrets-webhook`             |
| service.type                             | webhook service type                                                         | `ClusterIP`                         |
| tolerations                              | tolerations to add                                                           | `[]`                                |
| rbac.enabled                             | use rbac                                                                     | `true`                              |
| rbac.psp.enabled                         | use pod security policy                                                      | `false`                             |
| env.VAULT_IMAGE                          | vault image                                                                  | `vault:latest`                      |
| env.VAULT_ENV_IMAGE                      | vault-env image                                                              | `banzaicloud/vault-env:latest`      |
| volumes                                  | extra volume definitions                                                     | `[]`                                |
| volumeMounts                             | extra volume mounts                                                          | `[]`                                |
| configMapMutation                        | enable injecting values from Vault to ConfigMaps                             | `false`                             |
| podDisruptionBudget.enabled              | enable PodDisruptionBudget for Kong                                          | `false`                             |
| podDisruptionBudget.minAvailable         | represents the number of Pods that must be available (integer or percentage) | `1`                                 |
| metrics.enabled                          | enable serving metrics                                                       | `false`                             |
| metrics.port                             | port to serving metrics                                                      | `8444`                              |
| metrics.serviceMonitor.enabled           | enable monitoring via Prometheus Operator (`metrics.enabled` must be `true`) | `false`                             |
| metrics.serviceMonitor.scheme            | HTTP scheme to use for scraping                                              | `https`                             |
| metrics.serviceMonitor.tlsConfig         | TLS configuration to use when scraping the endpoint                          | `{insecureSkipVerify: true}`        |
| metrics.serviceMonitor.scrapeInterval    | interval between consecutive scrapes                                         | `30s`                               |
| metrics.serviceMonitor.metricRelabelings | [MetricRelabelConfigs](https://github.com/coreos/prometheus-operator/blob/master/Documentation/api.md#relabelconfig) to apply to samples before ingestion                    | `[]`                                |
| metrics.serviceMonitor.relabelings       | [RelabelConfigs](https://github.com/coreos/prometheus-operator/blob/master/Documentation/api.md#relabelconfig) to apply to samples before scraping                           | `[]`                                |
| metrics.serviceMonitor.additionalLabels  | additional labels for ServiceMonitor                                         | `{}`                                |
