package relay

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// Manual and scheduled detection runs are charged by Sub2API exactly like any
// other request. The accounting is captured after the run, with a short retry
// window, so operators can see what the detectors cost without opening a
// station request per history row.
const (
	degradationUsageSlackBefore = 2 * time.Minute
	degradationUsageSlackAfter  = 3 * time.Minute
	degradationUsageAttempts    = 3
	degradationUsageRetryDelay  = 4 * time.Second
	degradationUsageSyncTimeout = 60 * time.Second
	degradationUsageLookupLimit = 100
	// A busy group can log more than one page inside the run window, so the
	// lookup walks newest-first pages until it passes the window start.
	degradationUsageLookupPages = 10
	// Group runs call Sub2API with a key owned by this built-in admin. It is the
	// requester filter when the key owner could not be resolved for a run.
	defaultDegradationRequesterEmail = "admin@sub2api.local"
)

// degradationUsageJob describes one finished run. It is deliberately free of
// request state so the lookup survives a closed dialog or browser tab.
type degradationUsageJob struct {
	Scope             string
	StationID         uint
	GroupID           int64
	AccountID         int64
	ResultID          uint
	Models            []string
	StartedAt         time.Time
	FinishedAt        time.Time
	FallbackAccountID int64
	// RequesterUserID/RequesterLabel identify the Sub2API admin user whose key
	// issued the group run. They narrow usage rows to this run's calls; they are
	// never recorded as the serving account.
	RequesterUserID int64
	RequesterLabel  string
}

// startDegradationUsageSync runs the lookup detached from the request context.
func (s *Service) startDegradationUsageSync(job degradationUsageJob) {
	if s == nil || s.stations == nil || s.usageSync == nil || job.ResultID == 0 || job.StationID == 0 || job.StartedAt.IsZero() {
		return
	}
	go func() {
		// A bookkeeping failure must never take the process down with it.
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), degradationUsageSyncTimeout)
		defer cancel()
		s.usageSync(ctx, job)
	}()
}

func (s *Service) syncDegradationUsage(ctx context.Context, job degradationUsageJob) {
	usage := storage.DegradationUsage{}
	var err error
	for attempt := 0; attempt < degradationUsageAttempts; attempt++ {
		usage, err = s.lookupDegradationUsage(ctx, job)
		if err == nil && usage.Found {
			break
		}
		if attempt+1 >= degradationUsageAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(degradationUsageRetryDelay):
		}
	}
	if err != nil && !usage.Found {
		// A failed lookup still records the fallback owner so the history row is
		// not empty; a later run of the sync can fill the cost in.
		usage = storage.DegradationUsage{CostMultiplier: 1}
	}
	if usage.UsedAccountID == 0 && usage.UsedAccountName == "" {
		// Only account runs know their serving account up front. A group run
		// without a matching usage row stays unattributed instead of showing the
		// requesting admin as if it were the upstream account.
		usage.UsedAccountID = job.FallbackAccountID
	}
	if usage.UsedAccountName == "" && usage.UsedAccountID > 0 {
		// Resolve the name here rather than in the run path so detection
		// latency stays independent of this bookkeeping.
		if account, accountErr := s.stations.FindAccountByExternalID(job.StationID, usage.UsedAccountID); accountErr == nil {
			usage.UsedAccountName = account.Name
		}
	}
	if usage.CostMultiplier <= 0 {
		usage.CostMultiplier = 1
	}
	usage.SyncedAt = time.Now()
	_ = s.stations.CompleteDegradationUsage(ctx, job.Scope, job.ResultID, usage)
}

// lookupDegradationUsage reads the station usage rows that belong to one run and
// attributes them to the upstream account Sub2API actually used.
func (s *Service) lookupDegradationUsage(ctx context.Context, job degradationUsageJob) (storage.DegradationUsage, error) {
	usage := storage.DegradationUsage{}
	if s.cipher == nil {
		return usage, errors.New("站点凭据不可用")
	}
	station, err := s.stations.FindByID(job.StationID)
	if err != nil {
		return usage, err
	}
	key, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return usage, err
	}
	since := job.StartedAt.Add(-degradationUsageSlackBefore)
	until := job.FinishedAt.Add(degradationUsageSlackAfter)
	items, err := s.fetchDegradationUsageRows(ctx, station.BaseURL, key, job, since, until)
	if err != nil {
		return usage, err
	}
	models := map[string]struct{}{}
	for _, model := range job.Models {
		if model = strings.ToLower(strings.TrimSpace(model)); model != "" {
			models[model] = struct{}{}
		}
	}
	type accountTotal struct {
		name  string
		count int
	}
	accounts := map[int64]accountTotal{}
	baseCost, charged := 0.0, 0.0
	var inputTokens, outputTokens int64
	matched := 0
	requester := degradationRequester(job)
	for _, item := range items {
		created, ok := parseMonitorTime(item.CreatedAt)
		if !ok || created.Before(since) || created.After(until) {
			continue
		}
		if len(models) > 0 {
			model := strings.ToLower(strings.TrimSpace(item.Model))
			requested := strings.ToLower(strings.TrimSpace(item.RequestedModel))
			if _, ok := models[model]; !ok {
				if _, ok := models[requested]; !ok {
					continue
				}
			}
		}
		if job.AccountID > 0 && item.AccountID > 0 && item.AccountID != job.AccountID {
			continue
		}
		if job.AccountID == 0 && job.GroupID > 0 && item.GroupID > 0 && item.GroupID != job.GroupID {
			continue
		}
		if job.AccountID == 0 && !requester.matches(item) {
			continue
		}
		matched++
		inputTokens += item.InputTokens
		outputTokens += item.OutputTokens
		baseCost += item.TotalCost
		charged += item.ActualCost
		entry := accounts[item.AccountID]
		entry.count++
		if entry.name == "" {
			entry.name = strings.TrimSpace(item.AccountName)
		}
		accounts[item.AccountID] = entry
	}
	if matched == 0 {
		return usage, nil
	}
	// Pools can serve a run from several accounts; the newest/heaviest one is the
	// meaningful "which account was called" answer and drives the multiplier.
	bestID, best := int64(0), accountTotal{}
	for id, entry := range accounts {
		if entry.count > best.count || (entry.count == best.count && id > bestID) {
			bestID, best = id, entry
		}
	}
	multiplier, multiplierErr := s.accountCostMultiplier(ctx, job.StationID, bestID, job.GroupID)
	if multiplierErr != nil || multiplier <= 0 {
		multiplier = 1
	}
	usage.Found = true
	usage.UsedAccountID = bestID
	usage.UsedAccountName = best.name
	usage.InputTokens = inputTokens
	usage.OutputTokens = outputTokens
	usage.BaseCost = baseCost
	usage.CostMultiplier = multiplier
	if charged > 0 {
		usage.ActualCost = charged
	} else {
		// Older stations return only the base cost; apply the run account's
		// actual multiplier so the column keeps one meaning everywhere.
		usage.ActualCost = baseCost * multiplier
	}
	return usage, nil
}

// fetchDegradationUsageRows reads newest-first usage pages until the oldest row
// on a page predates the run window, so a busy group never pushes this run's
// rows past the first page.
func (s *Service) fetchDegradationUsageRows(ctx context.Context, baseURL, key string, job degradationUsageJob, since, until time.Time) ([]remoteUsage, error) {
	items := make([]remoteUsage, 0, degradationUsageLookupLimit)
	for pageNumber := 1; pageNumber <= degradationUsageLookupPages; pageNumber++ {
		var page remoteUsagePage
		if err := s.get(ctx, degradationUsageEndpoint(baseURL, since, until, job, pageNumber), key, &page); err != nil {
			if pageNumber == 1 {
				return nil, err
			}
			break // Earlier pages already cover the newest rows of the window.
		}
		items = append(items, page.Items...)
		if len(page.Items) == 0 || (page.Pages > 0 && pageNumber >= page.Pages) {
			break
		}
		if oldest, ok := parseMonitorTime(page.Items[len(page.Items)-1].CreatedAt); ok && oldest.Before(since) {
			break
		}
	}
	return items, nil
}

// degradationRequesterFilter keeps only usage rows issued by the admin key of a
// group run. Rows without any requester information are kept, because older
// Sub2API builds omit the user fields and the group/time/model filters still
// apply to them.
type degradationRequesterFilter struct {
	userID int64
	labels map[string]struct{}
}

func degradationRequester(job degradationUsageJob) degradationRequesterFilter {
	filter := degradationRequesterFilter{userID: job.RequesterUserID, labels: map[string]struct{}{}}
	for _, label := range []string{job.RequesterLabel, defaultDegradationRequesterEmail} {
		if label = strings.ToLower(strings.TrimSpace(label)); label != "" {
			filter.labels[label] = struct{}{}
		}
	}
	return filter
}

func (f degradationRequesterFilter) matches(item remoteUsage) bool {
	if f.userID > 0 && item.UserID > 0 {
		return item.UserID == f.userID
	}
	email, name := strings.ToLower(strings.TrimSpace(item.UserEmail)), strings.ToLower(strings.TrimSpace(item.UserName))
	if email == "" && name == "" {
		return true
	}
	_, emailOK := f.labels[email]
	_, nameOK := f.labels[name]
	return (email != "" && emailOK) || (name != "" && nameOK)
}

func (s *Service) accountCostMultiplier(ctx context.Context, stationID uint, accountID int64, groupID int64) (float64, error) {
	if accountID > 0 {
		accounts, err := s.stations.ListAccounts(stationID)
		if err != nil {
			return 1, err
		}
		for _, account := range accounts {
			if account.ExternalID != accountID || account.RateMultiplier == nil {
				continue
			}
			// The risk pipeline stores the account's upstream charge multiplier;
			// a zero or negative probe result means "unknown", not "free".
			if *account.RateMultiplier > 0 {
				return *account.RateMultiplier, nil
			}
			return 1, nil
		}
	}
	// Group runs have no account of their own; the group multiplier is the best
	// available fallback when the charged account is no longer listed.
	if groupID > 0 {
		groups, err := s.stations.ListGroups(stationID)
		if err != nil {
			return 1, err
		}
		for _, group := range groups {
			if group.ExternalID == groupID && group.RateMultiplier > 0 {
				return group.RateMultiplier, nil
			}
		}
	}
	return 1, nil
}

func degradationUsageEndpoint(baseURL string, since, until time.Time, job degradationUsageJob, pageNumber int) string {
	groupID, accountID := job.GroupID, job.AccountID
	if pageNumber < 1 {
		pageNumber = 1
	}
	query := []string{
		"page=" + strconv.Itoa(pageNumber),
		"page_size=" + strconv.Itoa(degradationUsageLookupLimit),
		"sort_by=created_at",
		"sort_order=desc",
		"start_date=" + since.In(time.Local).Format("2006-01-02"),
		"end_date=" + until.In(time.Local).Format("2006-01-02"),
	}
	if groupID > 0 && accountID == 0 {
		query = append(query, "group_id="+strconv.FormatInt(groupID, 10))
	}
	if accountID > 0 {
		query = append(query, "account_id="+strconv.FormatInt(accountID, 10))
	} else if job.RequesterUserID > 0 {
		// The admin endpoint narrows by requester; the client-side filter still
		// applies when a station ignores this parameter.
		query = append(query, "user_id="+strconv.FormatInt(job.RequesterUserID, 10))
	}
	return strings.TrimRight(baseURL, "/") + "/api/v1/admin/usage?" + strings.Join(query, "&")
}
