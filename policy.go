package gitops_terraform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/hashicorp/vault/sdk/logical"

	trdlGit "github.com/trublast/vault-plugin-gitops-terraform/pkg/git"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git_repository"
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

	// Clone repository and checkout to specific commit
	gitRepo, err := b.cloneRepositoryAtCommit(ctx, storage, config, hashCommit)
	if err != nil {
		return fmt.Errorf("unable to clone repository at commit %q: %w", hashCommit, err)
	}

	// Read policy files from policies directory
	policies, err := b.readPolicyFiles(gitRepo)
	if err != nil {
		return fmt.Errorf("unable to read policy files: %w", err)
	}

	if len(policies) == 0 {
		b.Logger().Info("No policy files found in policies directory, skipping policy application")
	} else {
		b.Logger().Info(fmt.Sprintf("Found %d policy files to apply", len(policies)))
		// Apply policies to Vault
		if err := b.applyPoliciesToVault(ctx, storage, policies); err != nil {
			return fmt.Errorf("unable to apply policies to Vault: %w", err)
		}
	}

	// Cleanup: memory storage will be garbage collected when gitRepo goes out of scope
	// Explicitly set to nil to help GC
	gitRepo = nil

	// Store finished commit only after successful processing
	return storeLastFinishedCommit(ctx, storage, hashCommit)
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

// readPolicyFiles reads all HCL policy files from policies directory
func (b *backend) readPolicyFiles(gitRepo *git.Repository) (map[string]string, error) {
	policies := make(map[string]string)

	err := trdlGit.ForEachWorktreeFile(gitRepo, func(filePath, link string, fileReader io.Reader, info os.FileInfo) error {
		// Skip if not in policies directory
		if !strings.HasPrefix(filePath, "policies/") {
			return nil
		}

		// Skip directories and symlinks
		if info.IsDir() || link != "" {
			return nil
		}

		// Only process .hcl files
		if !strings.HasSuffix(filePath, ".hcl") {
			return nil
		}

		// Get policy name from filename (without .hcl extension)
		policyName := strings.TrimSuffix(filepath.Base(filePath), ".hcl")
		if policyName == "" {
			return nil
		}

		// Read file content
		content, err := io.ReadAll(fileReader)
		if err != nil {
			return fmt.Errorf("reading policy file %q: %w", filePath, err)
		}

		policies[policyName] = string(content)
		b.Logger().Debug(fmt.Sprintf("Read policy file: %q (name: %q)", filePath, policyName))
		return nil
	})

	if err != nil {
		return nil, err
	}

	return policies, nil
}

// applyPoliciesToVault sends policies to Vault API
func (b *backend) applyPoliciesToVault(ctx context.Context, storage logical.Storage, policies map[string]string) error {
	vaultConfig, err := vault_client.GetConfig(ctx, storage, b.Logger())
	if err != nil {
		return fmt.Errorf("unable to get vault client configuration: %w", err)
	}

	vaultAddr := vaultConfig.VaultAddr
	if vaultAddr == "" {
		return fmt.Errorf("vault_addr is not set in configuration")
	}

	vaultToken := vaultConfig.VaultToken
	if vaultToken == "" {
		return fmt.Errorf("vault_token is not set in configuration")
	}

	// Remove trailing slash from vaultAddr
	vaultAddr = strings.TrimSuffix(vaultAddr, "/")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	for policyName, policyContent := range policies {
		url := fmt.Sprintf("%s/v1/sys/policies/acl/%s", vaultAddr, policyName)

		requestBody := map[string]string{
			"policy": policyContent,
		}

		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshaling policy %q request: %w", policyName, err)
		}

		req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("creating request for policy %q: %w", policyName, err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Vault-Token", vaultToken)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("sending request for policy %q: %w", policyName, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("vault API returned status %d for policy %q: %s", resp.StatusCode, policyName, string(body))
		}

		b.Logger().Info(fmt.Sprintf("Successfully applied policy: %q", policyName))
	}

	return nil
}
