# vault-plugin-gitops-terraform

⚠️ WORK IN PROGRESS

Плагин мониторит git-репозиторий на наличие новых коммитов. При наличии новых коммитов, подписанных необходимым количеством подписей, применяет конфигурацию

##

## Сборка

```bash
go build -o gitops-terraform cmd/gitops-terraform/main.go
```

## Загрузка плагина в Vault

```bash
SHA=$(sha256sum $PWD/gitops-terraform | awk '{print $1;}')
vault plugin register -command gitops-terraform -sha256 $SHA -version=v0.0.1 secret gitops-terraform
vault secrets enable gitops-terraform
```

## Настройка

Добавить репозиторий для мониторинга

```bash
vault write gitops-terraform/configure/git_repository \
      git_repo_url="https://gitlab.com/user/private-repo.git" \
      required_number_of_verified_signatures_on_commit=1 \
      git_poll_period=1m
```

Если репозиторий приватный, настроить учетную запись для доступа

```bash
vault write gitops-terraform/configure/git_credential \
      username=token \
      password=glpat-EAEAEAEAEK4SmS7Xmh4XP3m86MQp1OjE0CA.00.000123456
```

Создать ключи для подписи

```bash
gpg --quick-generate-key "key1 <key1@example.com>" rsa4096
gpg --quick-generate-key "key2 <key2@example.com>" rsa4096
```

Экспортировать публичные части ключей

```bash
gpg --armor --output key1.pgp --export key1
gpg --armor --output key2.pgp --export key2
```

Загрузить полученные ключи в Vault

```bash
vault write gitops-terraform/configure/trusted_pgp_public_key name=key1 public_key=@key1.pgp
vault write gitops-terraform/configure/trusted_pgp_public_key name=key2 public_key=@key2.pgp
```

## Подпись

Установить [git-signatures](https://github.com/werf/3p-git-signatures)
*Можно просто скопировать файл bin/git-signatures*

Скачать репозиторий

```bash
git clone https://gitlab.com/user/private-repo.git
cd private-repo
```

Посмотреть список ключей

```bash
gpg --list-key
```

Добавить ключ для подписи

```bash
git config user.signingKey <KEY_ID>
# Например: git config user.signingKey 0C3AAAA10E30D5F3
```

Добавить произвольный коммит и подписать его

```bash
date > .demo
git add .demo
git commit -m 'demo commit'
git signatures add
```

Проверить подпись

```bash
git signatures show
```

Ожидаемый вывод

```text
 Public Key ID    | Status     | Trust     | Date                         | Signer Name
=====================================================================================================
 0C3AAAA10E30D5F3 | VALIDSIG   | ULTIMATE  | Пн 22 дек 2025 20:19:33 MSK | key1 <key1@example.com>
```

Запушить изменения

```bash
git signatures push
```

## Выключение плагина

```bash
vault secrets disable gitops-terraform
vault plugin deregister -version=v0.0.1 secret gitops-terraform
```

# Структура GIT репозитория

```text
/
├── auth/
│   ├── approle
│   │   └── role
│   │       ├── role1.json
│   │       └── role2.json
│   └── kube-cluster1
│       └── role
│           ├── kuberole1.json
│           ├── kuberole2.json
│           └── kuberole3.json
└── policies/
    ├── policy-1.hcl
    └── policy-2.hcl
```
