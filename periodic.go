// Main workflow of the flant_gitops.
// Check is new commit signed by specific amount of PGP
// run Terraform job
// check statuses

// The base of workflow consistency:
// 1) new commit should go through last_started_commit -> {task for commit at task_manager} -> last_pushed_to_k8s_commit -> last_k8s_finished_commit
// 2) changes last_started_commit -> last_pushed_to_k8s_commit -> last_k8s_finished_commit are written only in main goroutine
// 3) action of created by commit task should finish as succeeded or failed
// 4) job at kube should be eventually terminated (by success/failed/timed out)
// trick: only one place to write data to storage

// Conditions for became new record of last_started_commit:
// 1) last_started_commit = last_pushed_to_k8s_commit = last_k8s_finished_commit
// 2) new  suitable commit at git

// Conditions for change last_pushed_to_k8s_commit:
// A) Normal task finish
// A1) task for last_started_commit is finished with any status (SUCCEEDED/FAILED/CANCELED)
// A2) kube has job with name last_started_commit
// B) Abnormal task finish
// B1) task for last_started_commit is finished with any status (SUCCEEDED/FAILED/CANCELED)
// B2) kube doesn't have job with name last_started_commit

// Conditions for change  last_terraform_finished_commit:
// A) Normal flow
// A1) Terraform has finished job with name last_applied_commit
// B) Abnormal flow
// B1) Terraform job failed

package gitops_terraform

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/vault/sdk/logical"

	"github.com/trublast/vault-plugin-gitops-terraform/pkg/git_repository"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/task_manager"
	"github.com/trublast/vault-plugin-gitops-terraform/pkg/util"
)

// for testability
var (
	systemClock                util.Clock = util.NewSystemClock()
	taskManagerServiceProvider            = task_manager.Service
)

const (
	//  store commit which is taken into work, but
	storageKeyLastStartedCommit  = "last_started_commit"
	storageKeyLastFinishedCommit = "last_finished_commit"
	lastPeriodicRunTimestampKey  = "last_periodic_run_timestamp"
)

func (b *backend) PeriodicTask(storage logical.Storage) error {
	ctx := context.Background()

	_, lastFinishedCommit, err := collectSavedWorkingCommits(ctx, storage)
	if err != nil {
		return err
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
	commitHash, err := git_repository.GitService(ctx, storage, b.Logger()).CheckForNewCommitFrom(lastFinishedCommit)
	if err != nil {
		return fmt.Errorf("obtaining new commit: %w", err)
	}

	if commitHash == nil {
		b.Logger().Debug("No new commits: finish periodic task")
		return nil
	}
	b.Logger().Info("obtain", "commitHash", *commitHash)

	if err := storeLastStartedCommit(ctx, storage, *commitHash); err != nil {
		return err
	}

	err = b.createTask(ctx, storage, *commitHash)
	if err != nil {
		return err
	}

	return updateLastRunTimeStamp(ctx, storage, newTimeStamp)
}

// createTask creates task and store gotten task_uuid
func (b *backend) createTask(ctx context.Context, storage logical.Storage, commitHash string) error {
	b.Logger().Warn("Run task is not implemented")
	// taskUUID, err := b.RunTask(ctx, storage, func(ctx context.Context, storage logical.Storage) error {
	// 	return b.processCommit(ctx, storage, commitHash)
	// })
	// if errors.Is(err, task_manager.ErrBusy) {
	// 	b.Logger().Warn(fmt.Sprintf("unable to add queue manager task: %s", err.Error()))
	// 	return nil
	// }
	// if err != nil {
	// 	return fmt.Errorf("unable to add queue manager task: %w", err)
	// }

	// b.Logger().Debug(fmt.Sprintf("Added new task with uuid %q for commitHash: %q", taskUUID, commitHash))
	// return taskManagerServiceProvider(storage, b.Logger()).SaveTask(ctx, taskUUID, commitHash)
	return nil
}

// collectSavedWorkingCommits gets, checks  and  returns : lastStartedCommit, lastPushedToK8sCommit, LastK8sFinishedCommit
// possible valid states: 1)  A, B, B  2) A, A, B 3) A, A, A
func collectSavedWorkingCommits(ctx context.Context, storage logical.Storage) (string, string, error) {
	lastStartedCommit, err := util.GetString(ctx, storage, storageKeyLastStartedCommit)
	if err != nil {
		return "", "", err
	}
	lastFinishedCommit, err := util.GetString(ctx, storage, storageKeyLastFinishedCommit)
	if err != nil {
		return "", "", err
	}
	return lastStartedCommit, lastFinishedCommit, nil
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

// processCommit aim action with retries
func (b *backend) processCommit(ctx context.Context, storage logical.Storage, hashCommit string) error {
	return nil
}

func storeLastStartedCommit(ctx context.Context, storage logical.Storage, hashCommit string) error {
	return util.PutString(ctx, storage, storageKeyLastStartedCommit, hashCommit)
}
