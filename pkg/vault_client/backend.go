package vault_client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

const (
	FieldNameVaultAddr  = "vault_addr"
	FieldNameVaultToken = "vault_token"

	StorageKeyConfiguration = "vault_client_configuration"
)

type Configuration struct {
	VaultAddr  string `structs:"vault_addr" json:"vault_addr"`
	VaultToken string `structs:"vault_token" json:"vault_token"`
}

type backend struct {
	// just for logger provider
	baseBackend *framework.Backend
}

func (b *backend) Logger() hclog.Logger {
	return b.baseBackend.Logger()
}

func Paths(baseBackend *framework.Backend) []*framework.Path {
	b := backend{
		baseBackend: baseBackend,
	}

	return []*framework.Path{
		{
			Pattern: "^configure/vault/?$",
			Fields: map[string]*framework.FieldSchema{
				FieldNameVaultAddr: {
					Type:        framework.TypeString,
					Description: "Vault API address. Required for CREATE, UPDATE.",
				},
				FieldNameVaultToken: {
					Type:        framework.TypeString,
					Description: "Vault token for API access. Optional for UPDATE.",
				},
			},

			Operations: map[logical.Operation]framework.OperationHandler{
				logical.CreateOperation: &framework.PathOperation{
					Callback: b.pathConfigureCreateOrUpdate,
					Summary:  "Create new vault client configuration.",
				},
				logical.UpdateOperation: &framework.PathOperation{
					Callback: b.pathConfigureCreateOrUpdate,
					Summary:  "Update the current vault client configuration.",
				},
				logical.ReadOperation: &framework.PathOperation{
					Callback: b.pathConfigureRead,
					Summary:  "Read the current vault client configuration.",
				},
				logical.DeleteOperation: &framework.PathOperation{
					Callback: b.pathConfigureDelete,
					Summary:  "Delete the current vault client configuration.",
				},
			},
			ExistenceCheck:  b.pathConfigExistenceCheck,
			HelpSynopsis:    configureHelpSyn,
			HelpDescription: configureHelpDesc,
		},
	}
}

// pathConfigExistenceCheck verifies if the configuration exists.
func (b *backend) pathConfigExistenceCheck(ctx context.Context, req *logical.Request, data *framework.FieldData) (bool, error) {
	out, err := req.Storage.Get(ctx, StorageKeyConfiguration)
	if err != nil {
		return false, fmt.Errorf("existence check failed: %w", err)
	}

	return out != nil, nil
}

func (b *backend) pathConfigureCreateOrUpdate(ctx context.Context, req *logical.Request, fields *framework.FieldData) (*logical.Response, error) {
	b.Logger().Debug("Vault client configuration started...")

	vaultAddr := fields.Get(FieldNameVaultAddr).(string)
	if vaultAddr == "" {
		return logical.ErrorResponse("%q field value should not be empty", FieldNameVaultAddr), nil
	}

	// Get existing configuration for UPDATE operation
	var existingConfig *Configuration
	if req.Operation == logical.UpdateOperation {
		existingConfig, _ = getConfiguration(ctx, req.Storage)
	}

	config := Configuration{
		VaultAddr: vaultAddr,
	}

	// For UPDATE: preserve existing token if new one is not provided
	// For CREATE: use provided token or empty string
	vaultToken := fields.Get(FieldNameVaultToken).(string)
	if req.Operation == logical.UpdateOperation && vaultToken == "" && existingConfig != nil {
		// Keep existing token if not provided in update
		config.VaultToken = existingConfig.VaultToken
	} else {
		// Use provided token (or empty for CREATE if not provided)
		config.VaultToken = vaultToken
	}

	{
		cfgData, cfgErr := json.MarshalIndent(config, "", "  ")
		b.Logger().Debug(fmt.Sprintf("Got Configuration (err=%v):\n%s", cfgErr, string(cfgData)))
	}

	if err := putConfiguration(ctx, req.Storage, config); err != nil {
		return nil, err
	}

	return nil, nil
}

func (b *backend) pathConfigureRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	b.Logger().Debug("Reading vault client configuration...")

	config, err := getConfiguration(ctx, req.Storage)
	if err != nil {
		return logical.ErrorResponse("Unable to get Configuration: %s", err), nil
	}
	if config == nil {
		return nil, nil
	}

	// Return only vault_addr, not vault_token
	data := map[string]interface{}{
		FieldNameVaultAddr: config.VaultAddr,
	}

	return &logical.Response{Data: data}, nil
}

func (b *backend) pathConfigureDelete(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	b.Logger().Debug("Deleting Configuration...")

	if err := deleteConfiguration(ctx, req.Storage); err != nil {
		return logical.ErrorResponse("Unable to delete Configuration: %s", err), nil
	}

	return nil, nil
}

func putConfiguration(ctx context.Context, storage logical.Storage, config Configuration) error {
	storageEntry, err := logical.StorageEntryJSON(StorageKeyConfiguration, config)
	if err != nil {
		return err
	}

	if err := storage.Put(ctx, storageEntry); err != nil {
		return err
	}

	return nil
}

func getConfiguration(ctx context.Context, storage logical.Storage) (*Configuration, error) {
	storageEntry, err := storage.Get(ctx, StorageKeyConfiguration)
	if err != nil {
		return nil, err
	}
	if storageEntry == nil {
		return nil, nil
	}

	var config Configuration
	if err := storageEntry.DecodeJSON(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func deleteConfiguration(ctx context.Context, storage logical.Storage) error {
	return storage.Delete(ctx, StorageKeyConfiguration)
}

// GetConfig returns the configuration for use in other packages
func GetConfig(ctx context.Context, storage logical.Storage, logger hclog.Logger) (*Configuration, error) {
	config, err := getConfiguration(ctx, storage)
	if err != nil {
		return nil, fmt.Errorf("unable to get Configuration: %w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("Configuration not set")
	}
	return config, nil
}

const (
	configureHelpSyn = `
Vault client configuration of the gitops_terraform backend.
`
	configureHelpDesc = `
The vault client configuration is used to connect to Vault API for applying policies.

This is vault client configuration for the gitops_terraform plugin.
`
)
