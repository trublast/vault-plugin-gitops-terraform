# gitops plugin policy
path "gitops/configure/git_repository" {
    capabilities = ["read", "update"]
}

path "gitops/configure/terraform" {
    capabilities = ["read", "create" ,"update", "delete"]
}

path "gitops/configure/git_credential" {
    capabilities = ["read", "create" ,"update", "delete"]
}

path "gitops/configure/trusted_pgp_public_key" {
    capabilities = ["read", "create" ,"update", "delete"]
}

path "gitops/configure/terraform" {
    capabilities = ["read", "create" ,"update", "delete"]
}
