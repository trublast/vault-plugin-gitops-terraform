// Main workflow of the flant_gitops.
// On each iteration:
// 1. Search from HEAD backwards to last_finished_commit (or initial_last_successful_commit if not set)
// 2. Find the first commit that has the required number of verified signatures
// 3. Call processCommit for that commit
// 4. If processCommit succeeds, save the commit as last_finished_commit
// 5. Next search will be from HEAD to the new last_finished_commit

package gitops_terraform

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/vault/sdk/logical"

	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git_repository"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/util"
)

// for testability
var (
	systemClock util.Clock = util.NewSystemClock()
)

const (
	storageKeyLastFinishedCommit = "last_finished_commit"
	lastPeriodicRunTimestampKey  = "last_periodic_run_timestamp"
	storageKeyProcessStatus      = "process_status"
)

func (b *backend) PeriodicTask(storage logical.Storage) error {
	ctx := context.Background()

	// Check and update vault token expire time if needed (using vault_client functions)
	if err := b.checkAndUpdateVaultTokenExpireTime(ctx, storage); err != nil {
		b.Logger().Warn(fmt.Sprintf("Failed to check/update vault token expire time: %v", err))
		// Don't fail the whole task, just log the error
	}

	// Get last finished commit
	lastFinishedCommit, err := util.GetString(ctx, storage, storageKeyLastFinishedCommit)
	if err != nil {
		return fmt.Errorf("unable to get last finished commit: %w", err)
	}

	return b.processGit(ctx, storage, lastFinishedCommit)
}

func (b *backend) processGit(ctx context.Context, storage logical.Storage, lastFinishedCommit string) error {
	config, err := git_repository.GetConfig(ctx, storage, b.Logger())
	if err != nil {
		return err
	}

	gitCheckintervalExceeded, err := checkExceedingInterval(ctx, storage, config.GitPollPeriod)
	if err != nil {
		return err
	}

	if !gitCheckintervalExceeded {
		b.Logger().Info("git poll interval not exceeded, finish periodic task")
		return nil
	}

	newTimeStamp := systemClock.Now()

	// Find first signed commit from HEAD backwards to lastFinishedCommit
	commitHash, err := git_repository.GitService(ctx, storage, b.Logger()).FindFirstSignedCommitFromHead(lastFinishedCommit)
	if err != nil {
		return fmt.Errorf("finding signed commit: %w", err)
	}

	if commitHash == nil {
		b.Logger().Debug("No signed commit found: finish periodic task")
		// TODO: do not store status when already same status
		if err := storeProcessStatusCommit(ctx, storage, "No new signed commit found"); err != nil {
			return fmt.Errorf("unable to store process status commit: %w", err)
		}
		return updateLastRunTimeStamp(ctx, storage, newTimeStamp)
	}

	b.Logger().Info("Found signed commit to process", "commitHash", *commitHash)

	// Process commit directly (no task manager)
	err = b.processCommit(ctx, storage, *commitHash)
	if err != nil {
		storeProcessStatusCommit(ctx, storage, fmt.Sprintf("FAILED processing commit %q: %s", *commitHash, err.Error()))
		return fmt.Errorf("processing commit %q: %w", *commitHash, err)
	}

	if err := storeProcessStatusCommit(ctx, storage, fmt.Sprintf("Successfully processed commit %q", *commitHash)); err != nil {
		return fmt.Errorf("unable to store process status commit: %w", err)
	}

	// Save last finished commit only if processCommit succeeded
	if err := storeLastFinishedCommit(ctx, storage, *commitHash); err != nil {
		return fmt.Errorf("unable to save last finished commit: %w", err)
	}

	b.Logger().Info("Successfully processed commit", "commitHash", *commitHash)

	return updateLastRunTimeStamp(ctx, storage, newTimeStamp)
}

// checkExceedingInterval returns true if more than interval were spent
func checkExceedingInterval(ctx context.Context, storage logical.Storage, interval time.Duration) (bool, error) {
	result := false
	lastRunTimestamp, err := util.GetInt64(ctx, storage, lastPeriodicRunTimestampKey)
	if err != nil {
		return false, err
	}
	if systemClock.Since(time.Unix(lastRunTimestamp, 0)) > interval {
		result = true
	}
	return result, nil
}

func updateLastRunTimeStamp(ctx context.Context, storage logical.Storage, timeStamp time.Time) error {
	return util.PutInt64(ctx, storage, lastPeriodicRunTimestampKey, timeStamp.Unix())
}

func storeLastFinishedCommit(ctx context.Context, storage logical.Storage, hashCommit string) error {
	return util.PutString(ctx, storage, storageKeyLastFinishedCommit, hashCommit)
}

func storeProcessStatusCommit(ctx context.Context, storage logical.Storage, status string) error {
	return util.PutString(ctx, storage, storageKeyProcessStatus, status)
}
