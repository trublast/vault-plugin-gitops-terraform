package git_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	goGit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/vault/sdk/logical"
	trdlGit "github.com/trublast/vault-plugin-gitops-terraform/pkg/git"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/pgp"
)

type gitCommitHash = string

type gitService struct {
	ctx     context.Context
	storage logical.Storage
	logger  hclog.Logger
}

func GitService(ctx context.Context, storage logical.Storage, logger hclog.Logger) gitService {
	return gitService{
		ctx:     ctx,
		storage: storage,
		logger:  logger,
	}
}

// FindFirstSignedCommitFromHead searches for the first signed commit starting from HEAD
// and going backwards until lastFinishedCommit.
// Returns the first commit that has the required number of verified signatures.
func (g gitService) FindFirstSignedCommitFromHead(lastFinishedCommit gitCommitHash) (*gitCommitHash, error) {
	config, err := GetConfig(g.ctx, g.storage, g.logger)
	if err != nil {
		return nil, err
	}

	// Determine the boundary commit: use lastFinishedCommit
	boundaryCommit := lastFinishedCommit

	// Clone git repository and get head commit
	g.logger.Debug(fmt.Sprintf("Cloning git repo %q branch %q", config.GitRepoUrl, config.GitBranch))
	gitRepo, headCommit, err := g.cloneGit(config)
	if err != nil {
		return nil, fmt.Errorf("cloning repository: %w", err)
	}

	// If boundary commit is set and equals HEAD, nothing to process
	if boundaryCommit != "" && boundaryCommit == headCommit {
		g.logger.Debug("Head commit equals boundary commit: no new commits to process")
		return nil, nil
	}

	// Get trusted PGP keys
	trustedPGPPublicKeys, err := pgp.GetTrustedPGPPublicKeys(g.ctx, g.storage)
	if err != nil {
		return nil, fmt.Errorf("unable to get trusted public keys: %w", err)
	}

	// Iterate from HEAD backwards until we find a signed commit or reach the boundary
	ref, err := gitRepo.Head()
	if err != nil {
		return nil, fmt.Errorf("unable to get HEAD: %w", err)
	}

	commit, err := gitRepo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("unable to get HEAD commit object: %w", err)
	}

	commitIter, err := gitRepo.Log(&goGit.LogOptions{From: commit.Hash})
	if err != nil {
		return nil, fmt.Errorf("unable to create commit iterator: %w", err)
	}
	defer commitIter.Close()

	for {
		c, err := commitIter.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Reached the end of history
				break
			}
			return nil, fmt.Errorf("error iterating commits: %w", err)
		}

		commitHash := c.Hash.String()

		// Stop if we reached the boundary commit
		if boundaryCommit != "" && commitHash == boundaryCommit {
			g.logger.Debug(fmt.Sprintf("Reached boundary commit %q, stopping search", boundaryCommit))
			break
		}

		// Verify commit signatures
		err = trdlGit.VerifyCommitSignatures(gitRepo, commitHash, trustedPGPPublicKeys, config.RequiredNumberOfVerifiedSignaturesOnCommit, g.logger)
		if err != nil {
			g.logger.Debug(fmt.Sprintf("Commit %q does not have required signatures: %s", commitHash, err.Error()))
			continue
		}

		// Found a commit with required signatures
		g.logger.Info(fmt.Sprintf("Found signed commit: %q", commitHash))
		return &commitHash, nil
	}

	// No signed commit found
	g.logger.Debug("No signed commit found in the search range")
	return nil, nil
}

// cloneGit clones specified repo, checkout specified branch and return head commit of branch
func (g gitService) cloneGit(config *Configuration) (*goGit.Repository, gitCommitHash, error) {
	gitCredentials, err := trdlGit.GetGitCredential(g.ctx, g.storage)
	if err != nil {
		return nil, "", fmt.Errorf("unable to get Git credentials Configuration: %s", err)
	}

	var cloneOptions trdlGit.CloneOptions
	{
		cloneOptions.BranchName = config.GitBranch
		// cloneOptions.RecurseSubmodules = goGit.DefaultSubmoduleRecursionDepth //

		if gitCredentials != nil && gitCredentials.Username != "" && gitCredentials.Password != "" {
			cloneOptions.Auth = &http.BasicAuth{
				Username: gitCredentials.Username,
				Password: gitCredentials.Password,
			}
		}

		if config.GitCACertificate != "" {
			cloneOptions.CABundle = []byte(config.GitCACertificate)
		}
	}

	var gitRepo *goGit.Repository
	if gitRepo, err = trdlGit.CloneInMemory(config.GitRepoUrl, cloneOptions); err != nil {
		return nil, "", fmt.Errorf("cloning in memeory: %w", err)
	}

	r, err := gitRepo.Head()
	if err != nil {
		return nil, "", err
	}
	headCommit := r.Hash().String()
	g.logger.Debug(fmt.Sprintf("Got head commit: %s", headCommit))
	return gitRepo, headCommit, nil
}

func GetConfig(ctx context.Context, storage logical.Storage, logger hclog.Logger) (*Configuration, error) {
	config, err := getConfiguration(ctx, storage)
	if err != nil {
		return nil, fmt.Errorf("unable to get Configuration: %w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("Configuration not set")
	}

	cfgData, _ := json.MarshalIndent(config, "", "  ") // nolint:errcheck
	logger.Debug(fmt.Sprintf("Got Configuration:\n%s", string(cfgData)))

	return config, nil
}
