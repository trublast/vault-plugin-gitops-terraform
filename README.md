# vault-plugin-gitops-terraform

⚠️ WORK IN PROGRESS

The plugin monitors a git repository for new commits. When new commits are found that are signed with the required number of signatures, it applies the configuration.

- Configuration is described in Terraform format.
- Terraform state is stored in Vault.
- Vault connection uses the address and token specified in the plugin configuration.
- Currently requires a renewable periodic token that will be automatically renewed 24 hours before expiration.
- Status and possible errors can be viewed via the `/v1/gitops-terraform/status` endpoint.
- It's assumed that the plugin loads the configuration itself, but this isn't required; you can manage another Vault.
- If you enable multiple plugins, you can manage different parts of the configuration accessible to the token from different repositories.

## Building

```bash
go build -o gitops-terraform cmd/gitops-terraform/main.go
```

## Loading the Plugin into Vault

```bash
SHA=$(sha256sum $PWD/gitops-terraform | awk '{print $1;}')
vault plugin register -command gitops-terraform -sha256 $SHA -version=v0.0.1 secret gitops-terraform
vault secrets enable gitops-terraform
```

## Configuration

Add a repository to monitor

```bash
vault write gitops-terraform/configure/git_repository \
      git_repo_url="https://gitlab.com/user/vault-gitops-configuration.git" \
      required_number_of_verified_signatures_on_commit=1 \
      git_poll_period=1m
```

If the repository is private, configure credentials for access

```bash
vault write gitops-terraform/configure/git_credential \
      username=token \
      password=glpat-EAEAEAEAEK4SmS7Xmh4XP3m86MQp1OjE0CA.00.000123456
```

Create keys for signing

```bash
gpg --quick-generate-key "key1 <key1@example.com>" rsa4096
gpg --quick-generate-key "key2 <key2@example.com>" rsa4096
```

Export public parts of the keys

```bash
gpg --armor --output key1.pgp --export key1
gpg --armor --output key2.pgp --export key2
```

Upload the obtained keys to Vault

```bash
vault write gitops-terraform/configure/trusted_pgp_public_key name=key1 public_key=@key1.pgp
vault write gitops-terraform/configure/trusted_pgp_public_key name=key2 public_key=@key2.pgp
```

Configuring plugin access to the Vault API

*temporary solution, token needs to be rotated*

```bash
TOKEN=$(vault token create -orphan -period=7d -policy=root -display-name="gitops-plugin" -field=token)
vault write gitops-terraform/configure/vault vault_addr=http://127.0.0.1:8200 vault_token=$TOKEN
```

## Signing

Install [git-signatures](https://github.com/werf/3p-git-signatures)
*You can simply copy the bin/git-signatures file*

Clone the repository or create new. See [example here](example-git)

```bash
git clone https://gitlab.com/user/vault-gitops-configuration.git
cd vault-gitops-configuration
```

View the list of keys

```bash
gpg --list-key
```

Add a key for signing

```bash
git config user.signingKey <KEY_ID>
# Example: git config user.signingKey 0C3AAAA10E30D5F3
```

Add an arbitrary commit and sign it

```bash
date > .demo
git add .demo
git commit -m 'demo commit'
git signatures add
```

Verify the signature

```bash
git signatures show
```

Expected output

```text
 Public Key ID    | Status     | Trust     | Date                         | Signer Name
=====================================================================================================
 0C3AAAA10E30D5F3 | VALIDSIG   | ULTIMATE  | Mon 22 Dec 2025 20:19:33 MSK | key1 <key1@example.com>
```

Push the changes

```bash
git push origin main
git signatures push
```

## Disabling the Plugin

```bash
vault secrets disable gitops-terraform
vault plugin deregister -version=v0.0.1 secret gitops-terraform
```
