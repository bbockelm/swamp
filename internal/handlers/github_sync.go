package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/github"
)

type syncRepoBranchKey struct {
	owner  string
	repo   string
	branch string
}

type syncRepoBranchTarget struct {
	representativePackageID string
	projectPackageIDs       map[string][]string
}

// StartGitHubAlertSyncLoop starts one background loop for polling GitHub alert
// state for packages that enabled GitHub sync.
func (h *Handler) StartGitHubAlertSyncLoop(ctx context.Context) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		return
	}

	go func() {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		// Base cadence plus jitter keeps calls spread out and avoids bursts.
		baseInterval := 5 * time.Minute

		for {
			initialDelay := time.Duration(rng.Intn(45)) * time.Second
			if !waitForContextOrDuration(ctx, initialDelay) {
				return
			}

			h.runGitHubAlertSyncPass(ctx, rng)

			next := baseInterval + time.Duration(rng.Int63n(int64(2*time.Minute))) - time.Minute
			if next < 2*time.Minute {
				next = 2 * time.Minute
			}
			if !waitForContextOrDuration(ctx, next) {
				return
			}
		}
	}()
}

func (h *Handler) runGitHubAlertSyncPass(ctx context.Context, rng *rand.Rand) {
	packages, err := h.queries.ListPackagesWithGitHubSync(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("GitHub sync poller: failed listing sync-enabled packages")
		return
	}
	if len(packages) == 0 {
		return
	}

	targets := make(map[syncRepoBranchKey]*syncRepoBranchTarget)
	for _, pkg := range packages {
		if pkg.GitHubOwner == "" || pkg.GitHubRepo == "" || pkg.GitBranch == "" {
			continue
		}
		key := syncRepoBranchKey{
			owner:  strings.ToLower(pkg.GitHubOwner),
			repo:   strings.ToLower(pkg.GitHubRepo),
			branch: pkg.GitBranch,
		}
		t := targets[key]
		if t == nil {
			t = &syncRepoBranchTarget{projectPackageIDs: map[string][]string{}}
			targets[key] = t
			t.representativePackageID = pkg.ID
		}
		t.projectPackageIDs[pkg.ProjectID] = append(t.projectPackageIDs[pkg.ProjectID], pkg.ID)
	}
	if len(targets) == 0 {
		return
	}

	keys := make([]syncRepoBranchKey, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].owner != keys[j].owner {
			return keys[i].owner < keys[j].owner
		}
		if keys[i].repo != keys[j].repo {
			return keys[i].repo < keys[j].repo
		}
		return keys[i].branch < keys[j].branch
	})

	limiter := time.NewTicker(1100 * time.Millisecond)
	defer limiter.Stop()

	totalAlerts := 0
	totalUpdates := int64(0)
	for _, key := range keys {
		target := targets[key]
		representative, err := h.queries.GetPackage(ctx, target.representativePackageID)
		if err != nil {
			continue
		}

		if !waitForContextOrDuration(ctx, time.Duration(rng.Intn(1500))*time.Millisecond) {
			return
		}
		if !waitForLimiterOrContext(ctx, limiter) {
			return
		}
		openAlerts, err := h.ghClient.ListCodeScanningAlertsForPackage(ctx, representative, "open")
		if err != nil {
			log.Debug().Err(err).Str("repo", key.owner+"/"+key.repo).Str("branch", key.branch).Msg("GitHub sync poller: open alert query failed")
			continue
		}

		if !waitForLimiterOrContext(ctx, limiter) {
			return
		}
		closedAlerts, err := h.ghClient.ListCodeScanningAlertsForPackage(ctx, representative, "closed")
		if err != nil {
			log.Debug().Err(err).Str("repo", key.owner+"/"+key.repo).Str("branch", key.branch).Msg("GitHub sync poller: closed alert query failed")
			continue
		}

		alertsByNumber := make(map[int64]github.CodeScanningAlert)
		for _, a := range openAlerts {
			alertsByNumber[a.Number] = a
		}
		for _, a := range closedAlerts {
			alertsByNumber[a.Number] = a
		}

		for _, alert := range alertsByNumber {
			totalAlerts++
			syncedAt := time.Now().UTC()
			for projectID, pkgIDs := range target.projectPackageIDs {
				updated, err := h.reconcileGitHubAlertForPackages(ctx, projectID, pkgIDs, alert, syncedAt)
				if err != nil {
					log.Debug().Err(err).Str("project_id", projectID).Int64("alert_number", alert.Number).Msg("GitHub sync poller: failed reconciling alert")
					continue
				}
				totalUpdates += updated
			}
		}
	}

	if totalAlerts > 0 {
		log.Info().Int("alerts", totalAlerts).Int64("updated_findings", totalUpdates).Msg("GitHub sync poller pass completed")
	}
}

func (h *Handler) reconcileGitHubAlertForPackages(ctx context.Context, projectID string, packageIDs []string, alert github.CodeScanningAlert, syncedAt time.Time) (int64, error) {
	updated, err := h.queries.UpdatePackageFindingsGitHubAlertByNumber(
		ctx,
		projectID,
		packageIDs,
		alert.Number,
		alert.HTMLURL,
		alert.State,
		alert.DismissedReason,
		alert.DismissedComment,
		alert.FixedAt,
		syncedAt,
	)
	if err != nil {
		return 0, err
	}
	if updated > 0 {
		return updated, nil
	}

	updated, err = h.queries.UpdatePackageFindingsGitHubAlertByLocation(
		ctx,
		projectID,
		packageIDs,
		alert.Rule.ID,
		alert.MostRecentInstance.Location.Path,
		alert.MostRecentInstance.Location.StartLine,
		alert.MostRecentInstance.Location.EndLine,
		alert.MostRecentInstance.CommitSHA,
		alert.Number,
		alert.HTMLURL,
		alert.State,
		alert.DismissedReason,
		alert.DismissedComment,
		alert.FixedAt,
		syncedAt,
	)
	if err != nil {
		return 0, err
	}
	return updated, nil
}

func waitForLimiterOrContext(ctx context.Context, ticker *time.Ticker) bool {
	select {
	case <-ctx.Done():
		return false
	case <-ticker.C:
		return true
	}
}

func waitForContextOrDuration(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (h *Handler) RunGitHubAlertSyncPassForTesting(ctx context.Context) error {
	if h == nil || h.ghClient == nil || !h.ghClient.Configured() {
		return fmt.Errorf("github client not configured")
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	h.runGitHubAlertSyncPass(ctx, rng)
	return nil
}
