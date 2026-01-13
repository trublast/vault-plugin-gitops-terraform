package vault_client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	vaultAddr = strings.TrimSuffix(vaultAddr, "/")
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

// GetValidConfig returns the configuration only if all validations pass (config exists, vault_addr and vault_token are set)
func GetValidConfig(ctx context.Context, storage logical.Storage, logger hclog.Logger) (*Configuration, error) {
	config, err := GetConfig(ctx, storage, logger)
	if err != nil {
		return nil, err
	}

	if config.VaultAddr == "" {
		return nil, fmt.Errorf("vault_addr is not set in configuration")
	}

	if config.VaultToken == "" {
		return nil, fmt.Errorf("vault_token is not set in configuration")
	}

	return config, nil
}

// TokenTTL represents token TTL information
type TokenTTL struct {
	TTL        time.Duration
	ExpireTime time.Time
}

// LookupTokenSelf performs /auth/token/lookup-self API call and returns TTL
func LookupTokenSelf(ctx context.Context, config *Configuration, logger hclog.Logger) (*TokenTTL, error) {

	url := fmt.Sprintf("%s/v1/auth/token/lookup-self", config.VaultAddr)

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("X-Vault-Token", config.VaultToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vault API returned status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data struct {
			TTL         int64  `json:"ttl"`
			CreationTTL int64  `json:"creation_ttl"`
			ExpireTime  string `json:"expire_time"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.New("decoding auth/token/lookup-self response")
	}

	ttl := time.Duration(response.Data.TTL) * time.Second

	var expireTime time.Time
	if response.Data.ExpireTime != "" {
		expireTime, err = time.Parse(time.RFC3339, response.Data.ExpireTime)
		if err != nil {
			// If expire_time parsing fails, calculate from TTL
			expireTime = time.Now().Add(ttl)
			logger.Debug(fmt.Sprintf("Failed to parse expire_time, calculated from TTL: %v", err))
		}
	} else {
		// If expire_time is not provided, calculate from TTL
		expireTime = time.Now().Add(ttl)
	}

	logger.Info(fmt.Sprintf("Token TTL: %v, Expire time: %v", ttl, expireTime))

	return &TokenTTL{
		TTL:        ttl,
		ExpireTime: expireTime,
	}, nil
}

// RenewTokenSelf performs /auth/token/renew-self API call and returns new TTL
func RenewTokenSelf(ctx context.Context, config *Configuration, logger hclog.Logger) (*TokenTTL, error) {

	url := fmt.Sprintf("%s/v1/auth/token/renew-self", config.VaultAddr)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Renew request body (empty body means use default increment)
	requestBody := map[string]interface{}{}
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Token", config.VaultToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vault API returned status %d when renewing token", resp.StatusCode)
	}

	var response struct {
		Auth struct {
			TTL         int64  `json:"lease_duration"`
			CreationTTL int64  `json:"creation_ttl"`
			ExpireTime  string `json:"expire_time"`
		} `json:"auth"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.New("decoding auth/token/renew-self response")
	}

	ttl := time.Duration(response.Auth.TTL) * time.Second

	var expireTime time.Time
	if response.Auth.ExpireTime != "" {
		expireTime, err = time.Parse(time.RFC3339, response.Auth.ExpireTime)
		if err != nil {
			// If expire_time parsing fails, calculate from TTL
			expireTime = time.Now().Add(ttl)
			logger.Debug(fmt.Sprintf("Failed to parse expire_time, calculated from TTL: %v", err))
		}
	} else {
		// If expire_time is not provided, calculate from TTL
		expireTime = time.Now().Add(ttl)
	}

	logger.Info(fmt.Sprintf("Token renewed, new TTL: %v, Expire time: %v", ttl, expireTime))

	return &TokenTTL{
		TTL:        ttl,
		ExpireTime: expireTime,
	}, nil
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
