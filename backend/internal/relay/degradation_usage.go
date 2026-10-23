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
)

// degradationUsageJob describes one finished run. It is deliberately free of
// request state so the lookup survives a closed dialog or browser tab.
type degradationUsageJob struct {
	Scope               string
	StationID           uint
	GroupID             int64
	AccountID           int64
	ResultID            uint
	Models              []string
	StartedAt           time.Time
	FinishedAt          time.Time
	FallbackAccountID   int64
	FallbackAccountName string
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
		usage.UsedAccountID, usage.UsedAccountName = job.FallbackAccountID, job.FallbackAccountName
		if usage.UsedAccountName == "" && usage.UsedAccountID > 0 {
			// Resolve the name here rather than in the run path so detection
			// latency stays independent of this bookkeeping.
			if account, accountErr := s.stations.FindAccountByExternalID(job.StationID, usage.UsedAccountID); accountErr == nil {
				usage.UsedAccountName = account.Name
			}
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
	endpoint := degradationUsageEndpoint(station.BaseURL, since, until, job.GroupID, job.AccountID)
	var page remoteUsagePage
	if err := s.get(ctx, endpoint, key, &page); err != nil {
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
	for _, item := range page.Items {
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

func degradationUsageEndpoint(baseURL string, since, until time.Time, groupID, accountID int64) string {
	query := []string{
		"page=1",
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
	}
	return strings.TrimRight(baseURL, "/") + "/api/v1/admin/usage?" + strings.Join(query, "&")
}
