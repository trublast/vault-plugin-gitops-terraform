package gitops_terraform

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/hashicorp/vault/sdk/logical"

	trdlGit "github.com/trublast/vault-plugin-gitops-terraform/pkg/git"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git_repository"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/terraform"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/vault_client"
)

// processCommit aim action with retries
func (b *backend) processCommit(ctx context.Context, storage logical.Storage, hashCommit string) error {
	b.Logger().Debug(fmt.Sprintf("Processing commit: %q", hashCommit))

	// Get git repository configuration
	config, err := git_repository.GetConfig(ctx, storage, b.Logger())
	if err != nil {
		return fmt.Errorf("unable to get git repository configuration: %w", err)
	}

	// Get vault client configuration
	vaultConfig, err := vault_client.GetConfig(ctx, storage)
	if err != nil {
		return fmt.Errorf("unable to get vault configuration: %w", err)
	}

	// Get terraform configuration
	tfConfig, err := terraform.GetConfig(ctx, storage)
	if err != nil {
		return fmt.Errorf("unable to get terraform configuration: %w", err)
	}

	// Clone repository and checkout to specific commit
	gitRepo, err := b.cloneRepositoryAtCommit(ctx, storage, config, hashCommit)
	if err != nil {
		return fmt.Errorf("unable to clone repository at commit %q: %w", hashCommit, err)
	}

	// Apply terraform configuration from repository using CLI
	terraformConfig := terraform.CLIConfig{
		VaultAddr:      vaultConfig.VaultAddr,
		VaultToken:     vaultConfig.VaultToken,
		VaultNamespace: vaultConfig.VaultNamespace,
		TfPath:         tfConfig.TfPath,
		Storage:        storage,
		Logger:         b.Logger(),
	}

	if err := terraform.ApplyTerraformFromRepo(ctx, gitRepo, terraformConfig); err != nil {
		return fmt.Errorf("unable to apply terraform configuration: %w", err)
	}

	// Cleanup: memory storage will be garbage collected when gitRepo goes out of scope
	// Explicitly set to nil to help GC
	gitRepo = nil

	// Return nil on success - lastFinishedCommit will be saved by caller
	return nil
}

// cloneRepositoryAtCommit clones repository and checks out to specific commit
func (b *backend) cloneRepositoryAtCommit(ctx context.Context, storage logical.Storage, config *git_repository.Configuration, commitHash string) (*git.Repository, error) {
	gitCredentials, err := trdlGit.GetGitCredential(ctx, storage)
	if err != nil {
		return nil, fmt.Errorf("unable to get Git credentials: %w", err)
	}

	var cloneOptions trdlGit.CloneOptions
	{
		cloneOptions.BranchName = config.GitBranch
		if gitCredentials != nil && gitCredentials.Username != "" && gitCredentials.Password != "" {
			cloneOptions.Auth = &gitHTTP.BasicAuth{
				Username: gitCredentials.Username,
				Password: gitCredentials.Password,
			}
		}
		if config.GitCACertificate != "" {
			cloneOptions.CABundle = []byte(config.GitCACertificate)
		}
	}

	gitRepo, err := trdlGit.CloneInMemory(config.GitRepoUrl, cloneOptions)
	if err != nil {
		return nil, fmt.Errorf("cloning repository: %w", err)
	}

	// Checkout to specific commit
	worktree, err := gitRepo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting worktree: %w", err)
	}

	commitHashObj := plumbing.NewHash(commitHash)
	err = worktree.Checkout(&git.CheckoutOptions{
		Hash: commitHashObj,
	})
	if err != nil {
		return nil, fmt.Errorf("checking out commit %q: %w", commitHash, err)
	}

	b.Logger().Debug(fmt.Sprintf("Checked out to commit: %q", commitHash))
	return gitRepo, nil
}
