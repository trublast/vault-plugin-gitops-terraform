# vault-plugin-gitops-terraform

⚠️ WORK IN PROGRESS

[English version](README.md)

Плагин мониторит git-репозиторий на наличие новых коммитов. При наличии новых коммитов, подписанных необходимым количеством подписей, применяет конфигурацию.

- Конфигурация описывается в формате Terraform.
- Стейт Terraform сохраняется в Vault.
- Для подключения к Vault используется адрес и токен, заданные в конфигурации плагина.
- В данный момент для работы требуется обновляемый periodic токен, который будет автоматически продлевается за 24 часа до окончания срока действия.
- Статус и возможные ошибки можно посмотреть через метод /v1/gitops/status.
- Предполагается, что плагин загружает конфигурацию сам в себя, но это не обязательно, можно управлять другим Vault.
- Если включить несколько плагинов, то можно из разный репозиториев управлять разными частями конфигурации, которые доступны токену.

## Сборка

```bash
go build -o gitops cmd/gitops-terraform/main.go
```

## Загрузка плагина в Vault

```bash
SHA=$(sha256sum $PWD/gitops | awk '{print $1;}')
vault plugin register -command gitops -sha256 $SHA -version=v0.0.1 secret gitops
vault secrets enable gitops
```

## Настройка

Добавить репозиторий для мониторинга

```bash
vault write gitops/configure/git_repository \
      git_repo_url="https://gitlab.com/user/vault-gitops-configuration.git" \
      required_number_of_verified_signatures_on_commit=1 \
      git_poll_period=1m
```

Если репозиторий приватный, настроить учетную запись для доступа

```bash
vault write gitops/configure/git_credential \
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
vault write gitops/configure/trusted_pgp_public_key/key1 public_key=@key1.pgp
vault write gitops/configure/trusted_pgp_public_key/key2 public_key=@key2.pgp
```

Настройка доступа плагина к API Vault

*временное решение, токен нужно ротейтить*

```bash
TOKEN=$(vault token create -orphan -period=7d -policy=root -display-name="gitops-plugin" -field=token)
vault write gitops/configure/vault vault_addr=http://127.0.0.1:8200 vault_token=$TOKEN
```

## Подпись

Установить [git-signatures](https://github.com/werf/3p-git-signatures)
*Можно просто скопировать файл bin/git-signatures*

Скачать ваш репозиторий конфигурацией или создать новый. Пример можно [посмотреть здесь](example-git)

```bash
git clone https://gitlab.com/user/vault-gitops-configuration.git
cd vault-gitops-configuration
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
git push origin main
git signatures push
```

## Выключение плагина

```bash
vault secrets disable gitops
vault plugin deregister -version=v0.0.1 secret gitops
```
