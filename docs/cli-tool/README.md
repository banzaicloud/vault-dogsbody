
# The CLI tool

The `bank-vaults` CLI tool is to help automate the setup and management of HashiCorp Vault.

Features:

- Initializes Vault and stores the root token and unseal keys in one of the followings:
  - AWS KMS keyring (backed by S3)
  - Azure Key Vault
  - Google Cloud KMS keyring (backed by GCS)
  - Alibaba Cloud KMS (backed by OSS)
  - Kubernetes Secrets With Encryption and Decryption using REST API Call 
  - Kubernetes Secrets (should be used only for development purposes)
  - Dev Mode (useful for `vault server -dev` dev mode Vault servers)
  - Files (backed by files, should be used only for development purposes)
- Automatically unseals Vault with these keys
- Continuously configures Vault with a YAML/JSON based external configuration (besides the [standard Vault configuration](https://www.vaultproject.io/docs/configuration/index.html))
  - If the configuration is updated Vault will be reconfigured
  - It supports configuring Vault secret engines, plugins, auth methods, and policies

The `bank-vaults` CLI command needs certain [cloud permissions](../cloud-permissions/README.md) to function properly (init, unseal, configuration).

## Example external Vault configuration

```yaml
# Allows creating policies in Vault which can be used later on in roles
# for the Kubernetes based authentication.
# See https://www.vaultproject.io/docs/concepts/policies.html for more information.
policies:
  - name: allow_secrets
    rules: path "secret/*" {
             capabilities = ["create", "read", "update", "delete", "list"]
           }
  - name: readonly_secrets
    rules: path "secret/*" {
             capabilities = ["read", "list"]
           }

# Allows configuring Auth Methods in Vault (Kubernetes and GitHub is supported now).
# See https://www.vaultproject.io/docs/auth/index.html for more information.
auth:
  - type: kubernetes
    # If you want to configure with specific kubernets service account instead of default service account
    # https://www.vaultproject.io/docs/auth/kubernetes.html
    # config:
    #   token_reviewer_jwt: eyJhbGciOiJSUzI1NiIsImtpZCI6IiJ9....
    #   kubernetes_ca_cert: |
    #     -----BEGIN CERTIFICATE-----
    #     ...
    #     -----END CERTIFICATE-----
    #   kubernetes_host: https://192.168.64.42:8443
    # Allows creating roles in Vault which can be used later on for the Kubernetes based
    # authentication.
    #  See https://www.vaultproject.io/docs/auth/kubernetes.html#creating-a-role for
    # more information.
    roles:
      # Allow every pod in the default namespace to use the secret kv store
      - name: default
        bound_service_account_names: default
        bound_service_account_namespaces: default
        policies: allow_secrets
        ttl: 1h

  # Allows creating roles in Vault which can be used later on for JWT based authentication
  # See https://www.vaultproject.io/docs/auth/jwt.html
  - type: jwt
    path: jwt
    config:
      oidc_discovery_url: https://myco.auth0.com/
    roles:
    - name: role1
      bound_audiences:
        - https://vault.plugin.auth.jwt.test
      user_claim: https://vault/user
      groups_claim: https://vault/groups
      policies: allow_secrets
      ttl: 1h

  # Allows creating team mappings in Vault which can be used later on for the GitHub
  # based authentication.
  # See https://www.vaultproject.io/docs/auth/github.html#configuration for
  # more information.
  - type: github
    # Make the auth provider visible in the web ui
    # See https://www.vaultproject.io/api/system/auth.html#config for more
    # information.
    options:
      listing_visibility: "unauth"
    config:
      organization: banzaicloud
    map:
      # Map the banzaicloud dev team on GitHub to the dev policy in Vault
      teams:
        dev: dev
      # Map myself to the root policy in Vault
      users:
        bonifaido: allow_secrets

  # Allows creating roles in Vault which can be used later on for AWS
  # IAM based authentication.
  # See https://www.vaultproject.io/docs/auth/aws.html for
  # more information.
  - type: aws
    # Make the auth provider visible in the web ui
    # See https://www.vaultproject.io/api/system/auth.html#config for more
    # information.
    options:
      listing_visibility: "unauth"
    config:
      access_key: VKIAJBRHKH6EVTTNXDHA
      secret_key: vCtSM8ZUEQ3mOFVlYPBQkf2sO6F/W7a5TVzrl3Oj
      iam_server_id_header_value: vault-dev.example.com # consider setting this to the Vault server's DNS name
    crossaccountrole:
    # Add cross account number and role to assume in the cross account
    # https://www.vaultproject.io/api/auth/aws/index.html#create-sts-role
    - sts_account: 12345671234
      sts_role_arn: arn:aws:iam::12345671234:role/crossaccountrole
    roles:
    # Add roles for AWS instances or principals
    # See https://www.vaultproject.io/api/auth/aws/index.html#create-role
    - name: dev-role-iam
      bound_iam_principal_arn: arn:aws:iam::123456789012:role/dev-vault
      policies: allow_secrets
      period: 1h
    - name: cross-account-role
      bound_iam_principal_arn: arn:aws:iam::12345671234:role/crossaccountrole
      policies: allow_secrets
      period: 1h

  # Allows creating roles in Vault which can be used later on for GCP
  # IAM based authentication.
  # See https://www.vaultproject.io/docs/auth/gcp.html for
  # more information.
  - type: gcp
    # Make the auth provider visible in the web ui
    # See https://www.vaultproject.io/api/system/auth.html#config for more
    # information.
    options:
      listing_visibility: "unauth"
    config:
      # Credentials context is service account's key. Can download when you create a key for service account. 
      # No need to manually create it. Just paste the json context as multiline yaml.
      credentials: -|
        {
          "type": "service_account",
          "project_id": "PROJECT_ID",
          "private_key_id": "KEY_ID",
          "private_key": "-----BEGIN PRIVATE KEY-----.....-----END PRIVATE KEY-----\n",
          "client_email": "SERVICE_ACCOUNT@PROJECT_ID.iam.gserviceaccount.com",
          "client_id": "CLIENT_ID",
          "auth_uri": "https://accounts.google.com/o/oauth2/auth",
          "token_uri": "https://oauth2.googleapis.com/token",
          "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
          "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/SERVICE_ACCOUNT%40PROJECT_ID.iam.gserviceaccount.com"
        }
    roles:
    # Add roles for gcp service account
    # See https://www.vaultproject.io/api/auth/gcp/index.html#create-role
    - name: user-role
      type: iam
      project_id: PROJECT_ID
      policies: "readonly_secrets"
      bound_service_accounts: "USER_SERVICE_ACCOUNT@PROJECT_ID.iam.gserviceaccount.com"
    - name: admin-role
      type: iam
      project_id: PROJECT_ID
      policies: "allow_secrets"
      bound_service_accounts: "ADMIN_SERVICE_ACCOUNT@PROJECT_ID.iam.gserviceaccount.com"

  # Allows creating group mappings in Vault which can be used later on for the LDAP
  # based authentication.
  # See https://www.vaultproject.io/docs/auth/ldap.html#configuration for
  # more information.
  # Start an LDAP testing server: docker run -it --rm -p 389:389 -e LDAP_TLS=false --name ldap osixia/openldap
  # Start an LDAP admin server: docker run -it --rm -p 6443:443 --link ldap:ldap -e PHPLDAPADMIN_LDAP_HOSTS=ldap -e PHPLDAPADMIN_LDAP_CLIENT_TLS=false osixia/phpldapadmin
  - type: ldap
    description: LDAP directory auth.
    # add mount options
    # See https://www.vaultproject.io/api/system/auth.html#config for more
    # information.
    options:
      listing_visibility: "unauth"
    config:
      url: ldap://localhost
      binddn: "cn=admin,dc=example,dc=org"
      bindpass: "admin"
      userattr: uid
      userdn: "ou=users,dc=example,dc=org"
      groupdn: "ou=groups,dc=example,dc=org"
    groups:
      # Map the banzaicloud dev team on GitHub to the dev policy in Vault
      developers:
        policies: allow_secrets
    # Map myself to the allow_secrets policy in Vault
    users:
      bonifaido:
        groups: developers
        policies: allow_secrets
  # Allows machines/apps to authenticate with Vault-defined roles.
  # See https://www.vaultproject.io/docs/auth/approle.html for more information
  - type: approle
    roles:
    - name: default
      policies: allow_secrets
      secret_id_ttl: 10m
      token_num_uses: 10
      token_ttl: 20m
      token_max_ttl: 30m
      secret_id_num_uses: 40
  # The azure auth method allows authentication against Vault using Azure Active Directory credentials. 
  # See https://www.vaultproject.io/docs/auth/azure.html for more information.
  - type: azure
    config:
      tenant_id: 00000000-0000-0000-0000-000000000000
      resource: https://vault-dev.example.com
      client_id: 00000000-0000-0000-0000-000000000000
      client_secret: 00000000-0000-0000-0000-000000000000
    roles:
    # Add roles for azure identities
    # See https://www.vaultproject.io/api/auth/azure/index.html#create-role
      - name: dev-mi
        policies: allow_secrets
        bound_subscription_ids: 
          - "00000000-0000-0000-0000-000000000000"
        bound_service_principal_ids: 
          - "00000000-0000-0000-0000-000000000000"
          
# Add environment variables. Please reference below `my-mysql` part for usage.
# This is a list of K8S env. You can reference K8S document for detail
# https://kubernetes.io/docs/tasks/inject-data-application/define-environment-variable-container/
# https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-environment-variables
envsConfig:
  - name: ROOT_USERNAME
    valueFrom:
      secretKeyRef:
        name: mysql-login
        key: user
  - name: ROOT_PASSWORD
    valueFrom:
      secretKeyRef:
        name: mysql-login
        key: password

# Allows configuring Secrets Engines in Vault (KV, Database and SSH is tested,
# but the config is free form so probably more is supported).
# See https://www.vaultproject.io/docs/secrets/index.html for more information.
secrets:
  # This plugin stores arbitrary secrets within the configured physical storage for Vault.
  # See https://www.vaultproject.io/docs/secrets/kv/index.html for
  # more information.
  - path: secret
    type: kv
    description: General secrets.
    options:
      version: 2
    configuration:
      config:
        - max_versions: 100
  # Mounts non-default plugin's path
  - path: ethereum-gateway
    type: plugin
    plugin_name: ethereum-plugin
    description: Immutability's Ethereum Wallet

  # This plugin stores database credentials dynamically based on configured roles for
  # the MySQL database.
  # See https://www.vaultproject.io/docs/secrets/databases/mysql-maria.html for
  # more information.
  - type: database
    description: MySQL Database secret engine.
    configuration:
      config:
        - name: my-mysql
          plugin_name: "mysql-database-plugin"
          connection_url: "{{username}}:{{password}}@tcp(127.0.0.1:3306)/"
          allowed_roles: [pipeline]
          username: "${env `ROOT_USERNAME`}" # Example how to read environment variables
          password: "${env `ROOT_PASSWORD`}"
      roles:
        - name: pipeline
          db_name: my-mysql
          creation_statements: "GRANT ALL ON *.* TO '{{name}}'@'%' IDENTIFIED BY '{{password}}';"
          default_ttl: "10m"
          max_ttl: "24h"

  # Create a named Vault role for signing SSH client keys.
  # See https://www.vaultproject.io/docs/secrets/ssh/signed-ssh-certificates.html#client-key-signing for
  # more information.
  - type: ssh
    path: ssh-client-signer
    description: SSH Client Key Signing.
    configuration:
      config:
        - name: ca
          generate_signing_key: "true"
      roles:
        - name: my-role
          allow_user_certificates: "true"
          allowed_users: "*"
          key_type: "ca"
          default_user: "ubuntu"
          ttl: "24h"

  # The RabbitMQ secrets engine generates user credentials dynamically based on configured permissions and virtual hosts.
  # See https://www.vaultproject.io/docs/secrets/rabbitmq/index.html
  # Start a RabbitMQ testing server: docker run -it --rm -p 15672:15672 rabbitmq:3.7-management-alpine
  - type: rabbitmq
    description: local-rabbit
    configuration:
      config:
        - name: connection
          connection_uri: "http://localhost:15672"
          username: guest
          password: guest
      roles:
        - name: prod_role
          vhosts: '{"/web":{"write": "production_.*", "read": "production_.*"}}'

  # The PKI secrets engine generates X.509 certificates
  # See https://www.vaultproject.io/docs/secrets/pki/index.html for more information
  - type: pki
    description: Vault PKI Backend
    config:
      default_lease_ttl: 168h
      max_lease_ttl: 720h    
    configuration:
      config:
      - name: urls
        issuing_certificates: https://vault.default:8200/v1/pki/ca
        crl_distribution_points: https://vault.default:8200/v1/pki/crl
      root/generate:
      - name: internal
        common_name: vault.default
      roles:
      - name: default
        allowed_domains: localhost,pod,svc,default
        allow_subdomains: true
        generate_lease: true
        ttl: 30m

  # The AWS secrets engine generates AWS access credentials dynamically based on IAM policies.
  # https://www.vaultproject.io/docs/secrets/aws/index.html
  - type: aws
    path: aws
    description: AWS Secret Backend
    configuration:
        config: 
          - name: root
            access_key: "${env `AWS_ACCESS_KEY_ID`}"
            secret_key: "${env `AWS_SECRET_ACCESS_KEY`}"
            region: us-east-1
        roles:
          - credential_type: iam_user
            policy_arns: arn-of-policy
            name: my-aws-role

# Identity Groups
# NOTE: Only external groups are supported at the moment through the use of group-aliases
# for supported authentication backends, like for example JWT, which will automatically match those aliases to groups returned by the backend
# the configuration files for the groups and group-aliases need to be parsed after the 
# authentication backend has already mounted. Ideally they should be in the same file to avoid 
# risk of errors
groups: 
  - name: admin
    policies:
      - admin
    metadata:
      admin: "true"
      priviliged: "true"
    type: external

group-aliases:
  - name: admin
    mountpath: jwt
    group: admin

# Registers a new plugin in Vault's plugin catalog. "plugin_directory" setting should be set it Vault server configuration
# and plugin binary should be present in plugin directory. Also, for some plugins readOnlyRootFilesystem Pod Security Policy
# should be disabled to allow RPC communication between plugin and Vault server via Unix socket
# See https://www.vaultproject.io/api/system/plugins-catalog.html and
#     https://github.com/hashicorp/go-plugin/blob/master/docs/internals.md for details.
plugins:
  - plugin_name: ethereum-plugin
    command: ethereum-vault-plugin --ca-cert=/vault/tls/client/ca.crt --client-cert=/vault/tls/server/server.crt --client-key=/vault/tls/server/server.key
    sha256: 62fb461a8743f2a0af31d998074b58bb1a589ec1d28da3a2a5e8e5820d2c6e0a
    type: secret

# Allows configuring Audit Devices in Vault (File, Syslog, Socket).
# See https://www.vaultproject.io/docs/audit/ for more information.
audit:
  - type: file
    description: "File based audit logging device"
    options:
      file_path: /tmp/vault.log

# Allows writing some secrets to Vault (useful for development purposes).
# See https://www.vaultproject.io/docs/secrets/kv/index.html for more information.
startupSecrets:
  - type: kv
    path: secret/data/accounts/aws
    data:
      data:
        AWS_ACCESS_KEY_ID: secretId
        AWS_SECRET_ACCESS_KEY: s3cr3t
```

The documentation of the Bank-Vaults project is available at the [Banzai Cloud Documentation Page](https://banzaicloud.com/docs/bank-vaults/).

