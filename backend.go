package gitops_terraform

import (
	"context"
	"fmt"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git_repository"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/pgp"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/vault_client"
	"github.com/werf/trdl/server/pkg/tasks_manager"
)

type backend struct {
	*framework.Backend
	TasksManager *tasks_manager.Manager
}

var _ logical.Factory = Factory

func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b, err := newBackend(conf)
	if err != nil {
		return nil, err
	}

	if conf == nil {
		return nil, fmt.Errorf("Configuration passed into backend is nil")
	}

	if err := b.SetupBackend(ctx, conf); err != nil {
		return nil, err
	}

	return b, nil
}

func newBackend(c *logical.BackendConfig) (*backend, error) {

	b := &backend{
		TasksManager: tasks_manager.NewManager(c.Logger),
		// AccessVaultClientProvider: accessVaultClientProvider,
	}

	baseBackend := &framework.Backend{
		BackendType: logical.TypeLogical,
		Help:        backendHelp,

		PeriodicFunc: func(ctx context.Context, req *logical.Request) error {
			return b.PeriodicTask(req.Storage)
		},
	}

	baseBackend.Paths = framework.PathAppend(
		git_repository.Paths(baseBackend),
		vault_client.Paths(baseBackend),
		git.CredentialsPaths(),
		pgp.Paths(),
	)

	b.Backend = baseBackend

	return b, nil
}

func (b *backend) SetupBackend(ctx context.Context, config *logical.BackendConfig) error {
	if err := b.Setup(ctx, config); err != nil {
		return err
	}

	// Set storage for TasksManager to use in callbacks
	// b.TasksManager.Storage = config.StorageView

	return nil
}

const (
	backendHelp = `
The gitops_terraform plugin starts an operator which waits for new commits in the configured git repository, verifies commit signatures by configured pgp keys, then executes configured commands in this new commit.
`
)
