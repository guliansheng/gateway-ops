package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	openAIStatusURL     = "https://status.openai.com/api/v2/summary.json"
	openAIComponentsURL = "https://status.openai.com/api/v2/components.json"
	openAIIncidentsURL  = "https://status.openai.com/api/v2/incidents.json"
	claudeStatusURL     = "https://status.claude.com/api/v2/summary.json"
	claudeIncidentsURL  = "https://status.claude.com/api/v2/incidents.json"
	statusFetchTimeout  = 12 * time.Second
	statusCacheTTL      = 30 * time.Second
	statusHistoryDays   = 60
	statusMaxBodyBytes  = 4 << 20
)

type publicServiceStatusCache struct {
	mu      sync.Mutex
	value   *PublicServiceStatusView
	expires time.Time
}

var serviceStatusCache publicServiceStatusCache

type PublicServiceStatusView struct {
	UpdatedAt time.Time             `json:"updated_at"`
	Days      int                   `json:"days"`
	Services  []PublicServiceStatus `json:"services"`
}
type PublicServiceStatus struct {
	ID                    string                         `json:"id"`
	Name                  string                         `json:"name"`
	URL                   string                         `json:"url"`
	HistoryURL            string                         `json:"history_url"`
	UpdatedAt             *time.Time                     `json:"updated_at,omitempty"`
	Status                PublicServiceStatusSummary     `json:"status"`
	Components            []PublicServiceStatusComponent `json:"components"`
	Groups                []PublicServiceStatusGroup     `json:"groups"`
	Incidents             []PublicServiceStatusIncident  `json:"incidents"`
	PastIncidents         []PublicServiceStatusIncident  `json:"past_incidents"`
	ScheduledMaintenances []PublicServiceStatusIncident  `json:"scheduled_maintenances"`
	Error                 string                         `json:"error,omitempty"`
}
type PublicServiceStatusSummary struct {
	Indicator   string `json:"indicator"`
	Description string `json:"description"`
}
type PublicServiceStatusComponent struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Status      string                   `json:"status"`
	Description string                   `json:"description,omitempty"`
	UpdatedAt   *time.Time               `json:"updated_at,omitempty"`
	History     []PublicServiceStatusDay `json:"history"`
}
type PublicServiceStatusGroup struct {
	ID          string                         `json:"id"`
	Name        string                         `json:"name"`
	Description string                         `json:"description,omitempty"`
	Status      string                         `json:"status"`
	Uptime      float64                        `json:"uptime"`
	History     []PublicServiceStatusDay       `json:"history"`
	Components  []PublicServiceStatusComponent `json:"components"`
}
type PublicServiceStatusDay struct {
	Date      string                    `json:"date"`
	Status    string                    `json:"status"`
	Incidents []PublicServiceStatusBlip `json:"incidents,omitempty"`
}
type PublicServiceStatusBlip struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Impact string `json:"impact"`
}
type PublicServiceStatusIncident struct {
	ID              string                              `json:"id"`
	Name            string                              `json:"name"`
	Status          string                              `json:"status"`
	Impact          string                              `json:"impact"`
	Shortlink       string                              `json:"shortlink,omitempty"`
	CreatedAt       *time.Time                          `json:"created_at,omitempty"`
	UpdatedAt       *time.Time                          `json:"updated_at,omitempty"`
	ResolvedAt      *time.Time                          `json:"resolved_at,omitempty"`
	Components      []PublicServiceIncidentComponent    `json:"components,omitempty"`
	IncidentUpdates []PublicServiceStatusIncidentUpdate `json:"incident_updates,omitempty"`
}
type PublicServiceIncidentComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type PublicServiceStatusIncidentUpdate struct {
	Body               string                                 `json:"body"`
	Status             string                                 `json:"status"`
	CreatedAt          *time.Time                             `json:"created_at,omitempty"`
	UpdatedAt          *time.Time                             `json:"updated_at,omitempty"`
	DisplayAt          *time.Time                             `json:"display_at,omitempty"`
	AffectedComponents []PublicServiceStatusAffectedComponent `json:"affected_components,omitempty"`
}
type PublicServiceStatusAffectedComponent struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	OldStatus string `json:"old_status"`
	NewStatus string `json:"new_status"`
}

type statusPageSummary struct {
	Page struct {
		URL       string    `json:"url"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"page"`
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components            []PublicServiceStatusComponent `json:"components"`
	Incidents             []PublicServiceStatusIncident  `json:"incidents"`
	ScheduledMaintenances []PublicServiceStatusIncident  `json:"scheduled_maintenances"`
}
type statusPageIncidents struct {
	Incidents []PublicServiceStatusIncident `json:"incidents"`
}
type statusPageComponents struct {
	Components []PublicServiceStatusComponent `json:"components"`
}
type statusTarget struct{ id, name, pageURL, historyURL, summaryURL, componentsURL, incidentsURL string }
type openAIGroupDefinition struct {
	ID, Name, Description string
	ComponentNames        []string
}

var openAIGroupDefinitions = []openAIGroupDefinition{
	{ID: "apis", Name: "APIs", Description: "OpenAI API 服务", ComponentNames: []string{"Chat Completions", "Responses", "Fine-tuning", "Embeddings", "Images", "Batch", "Audio", "Moderations", "Realtime", "Files", "Login", "Sora"}},
	{ID: "chatgpt", Name: "ChatGPT", Description: "ChatGPT 产品与工作区服务", ComponentNames: []string{"Conversations", "Login", "ChatGPT Work", "Codex in ChatGPT Desktop", "Compliance API", "Search", "File uploads", "Voice mode", "GPTs", "Image Generation", "Deep Research", "Agent", "ChatGPT Atlas", "Sites", "Connectors/Apps"}},
	{ID: "codex", Name: "Codex", Description: "Codex Web、API、CLI 与编辑器扩展", ComponentNames: []string{"Codex Web", "Codex API", "CLI", "VS Code extension"}},
	{ID: "fedramp", Name: "FedRAMP", ComponentNames: []string{"FedRAMP"}},
	{ID: "ads-platform", Name: "Ads Platform", ComponentNames: []string{"Ads Manager", "Ads API"}},
}

func registerPublicServiceStatus(g *gin.RouterGroup) { g.GET("/service-status", publicServiceStatus) }

func publicServiceStatus(c *gin.Context) {
	now := time.Now().UTC()
	serviceStatusCache.mu.Lock()
	if serviceStatusCache.value != nil && now.Before(serviceStatusCache.expires) {
		cached := *serviceStatusCache.value
		serviceStatusCache.mu.Unlock()
		c.JSON(http.StatusOK, gin.H{"data": cached})
		return
	}
	serviceStatusCache.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), statusFetchTimeout)
	defer cancel()
	targets := []statusTarget{{"openai", "OpenAI", "https://status.openai.com/", "https://status.openai.com/history", openAIStatusURL, openAIComponentsURL, openAIIncidentsURL}, {"claude", "Claude", "https://status.claude.com/", "https://status.claude.com/history", claudeStatusURL, "", claudeIncidentsURL}}
	results := make(chan PublicServiceStatus, len(targets))
	for _, target := range targets {
		target := target
		go func() { results <- fetchPublicServiceStatus(ctx, target, now) }()
	}
	byID := make(map[string]PublicServiceStatus, len(targets))
	for range targets {
		service := <-results
		byID[service.ID] = service
	}
	view := PublicServiceStatusView{UpdatedAt: now, Days: statusHistoryDays, Services: make([]PublicServiceStatus, 0, len(targets))}
	for _, target := range targets {
		view.Services = append(view.Services, byID[target.id])
	}
	serviceStatusCache.mu.Lock()
	serviceStatusCache.value = &view
	serviceStatusCache.expires = now.Add(statusCacheTTL)
	serviceStatusCache.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func fetchPublicServiceStatus(ctx context.Context, target statusTarget, now time.Time) PublicServiceStatus {
	service := PublicServiceStatus{ID: target.id, Name: target.name, URL: target.pageURL, HistoryURL: target.historyURL, Components: []PublicServiceStatusComponent{}, Groups: []PublicServiceStatusGroup{}, Incidents: []PublicServiceStatusIncident{}, PastIncidents: []PublicServiceStatusIncident{}, ScheduledMaintenances: []PublicServiceStatusIncident{}}
	var summary statusPageSummary
	if err := fetchStatusPage(ctx, target.summaryURL, &summary); err != nil {
		service.Status = PublicServiceStatusSummary{Indicator: "unknown", Description: "无法获取状态"}
		service.Error = "官方状态页暂时无法访问"
		return service
	}
	service.UpdatedAt = &summary.Page.UpdatedAt
	service.Status = PublicServiceStatusSummary{Indicator: summary.Status.Indicator, Description: summary.Status.Description}
	service.Components = summary.Components
	service.Incidents = summary.Incidents
	service.ScheduledMaintenances = summary.ScheduledMaintenances
	if target.componentsURL != "" {
		var components statusPageComponents
		if fetchStatusPage(ctx, target.componentsURL, &components) == nil && len(components.Components) > 0 {
			service.Components = components.Components
		}
	}
	allIncidents := append([]PublicServiceStatusIncident(nil), summary.Incidents...)
	var incidentPage statusPageIncidents
	if fetchStatusPage(ctx, target.incidentsURL, &incidentPage) == nil {
		allIncidents = incidentPage.Incidents
	}
	componentMap := make(map[string]PublicServiceStatusComponent, len(service.Components))
	for index := range service.Components {
		componentMap[service.Components[index].ID] = service.Components[index]
	}
	applyIncidentComponents(target.id, allIncidents, componentMap)
	cutoff := now.AddDate(0, -2, 0)
	service.Incidents, service.PastIncidents = splitServiceIncidents(allIncidents, cutoff)
	for index := range service.Components {
		service.Components[index].History = buildStatusHistory([]string{service.Components[index].ID}, allIncidents, now)
	}
	if service.ID == "openai" {
		service.Groups = buildOpenAIGroups(service.Components, allIncidents, now)
	} else {
		for _, component := range service.Components {
			service.Groups = append(service.Groups, PublicServiceStatusGroup{ID: component.ID, Name: component.Name, Description: component.Description, Status: component.Status, Uptime: historyUptime(component.History), History: component.History, Components: []PublicServiceStatusComponent{component}})
		}
	}
	return service
}

func splitServiceIncidents(incidents []PublicServiceStatusIncident, cutoff time.Time) ([]PublicServiceStatusIncident, []PublicServiceStatusIncident) {
	current, past := []PublicServiceStatusIncident{}, []PublicServiceStatusIncident{}
	for _, incident := range incidents {
		if strings.EqualFold(incident.Status, "resolved") || strings.EqualFold(incident.Status, "completed") {
			if incident.CreatedAt != nil && !incident.CreatedAt.Before(cutoff) {
				past = append(past, incident)
			}
			continue
		}
		current = append(current, incident)
	}
	sort.SliceStable(past, func(i, j int) bool { return incidentStart(past[i]).After(incidentStart(past[j])) })
	return current, past
}
func incidentStart(incident PublicServiceStatusIncident) time.Time {
	if incident.CreatedAt != nil {
		return *incident.CreatedAt
	}
	if incident.UpdatedAt != nil {
		return *incident.UpdatedAt
	}
	return time.Time{}
}

func applyIncidentComponents(serviceID string, incidents []PublicServiceStatusIncident, components map[string]PublicServiceStatusComponent) {
	for incidentIndex := range incidents {
		seen := map[string]bool{}
		for _, component := range incidents[incidentIndex].Components {
			if component.ID != "" {
				seen[component.ID] = true
			}
		}
		for _, update := range incidents[incidentIndex].IncidentUpdates {
			for _, affected := range update.AffectedComponents {
				name := affected.Name
				if name == "" {
					name = components[affected.Code].Name
				}
				if affected.Code != "" && name != "" && !seen[affected.Code] {
					incidents[incidentIndex].Components = append(incidents[incidentIndex].Components, PublicServiceIncidentComponent{ID: affected.Code, Name: name})
					seen[affected.Code] = true
				}
			}
		}
		if serviceID == "openai" && len(incidents[incidentIndex].Components) == 0 {
			for id, component := range components {
				if openAIIncidentMatchesComponent(incidents[incidentIndex].Name, component.Name) {
					incidents[incidentIndex].Components = append(incidents[incidentIndex].Components, PublicServiceIncidentComponent{ID: id, Name: component.Name})
				}
			}
		}
	}
}
func openAIIncidentMatchesComponent(incidentName, componentName string) bool {
	incident, component := strings.ToLower(incidentName), strings.ToLower(componentName)
	if strings.Contains(incident, component) {
		return true
	}
	aliases := map[string][]string{"conversations": {"chatgpt"}, "file uploads": {"file upload"}, "image generation": {"image generation", "images"}, "voice mode": {"voice"}, "codex web": {"codex"}, "codex api": {"codex"}, "cli": {"codex"}, "vs code extension": {"codex"}}
	for _, alias := range aliases[component] {
		if strings.Contains(incident, alias) {
			return true
		}
	}
	return false
}

func buildOpenAIGroups(components []PublicServiceStatusComponent, incidents []PublicServiceStatusIncident, now time.Time) []PublicServiceStatusGroup {
	byName := map[string]PublicServiceStatusComponent{}
	for _, component := range components {
		byName[component.Name] = component
	}
	groups := make([]PublicServiceStatusGroup, 0, len(openAIGroupDefinitions))
	for _, definition := range openAIGroupDefinitions {
		group := PublicServiceStatusGroup{ID: definition.ID, Name: definition.Name, Description: definition.Description, Status: "operational", Components: []PublicServiceStatusComponent{}}
		ids := []string{}
		for _, name := range definition.ComponentNames {
			if component, ok := byName[name]; ok {
				group.Components = append(group.Components, component)
				ids = append(ids, component.ID)
				group.Status = worseStatus(group.Status, component.Status)
			}
		}
		group.History = buildStatusHistory(ids, incidents, now)
		group.Uptime = historyUptime(group.History)
		groups = append(groups, group)
	}
	return groups
}
func buildStatusHistory(componentIDs []string, incidents []PublicServiceStatusIncident, now time.Time) []PublicServiceStatusDay {
	if len(componentIDs) == 0 {
		return make([]PublicServiceStatusDay, 0)
	}
	allowed := map[string]bool{}
	for _, id := range componentIDs {
		allowed[id] = true
	}
	days := make([]PublicServiceStatusDay, statusHistoryDays)
	for index := range days {
		day := now.AddDate(0, 0, index-(statusHistoryDays-1))
		days[index] = PublicServiceStatusDay{Date: day.Format("2006-01-02"), Status: "operational", Incidents: []PublicServiceStatusBlip{}}
	}
	for _, incident := range incidents {
		start := incidentStart(incident)
		if start.IsZero() || (len(allowed) > 0 && !incidentAffectsComponents(incident, allowed)) {
			continue
		}
		end := now
		if incident.ResolvedAt != nil {
			end = *incident.ResolvedAt
		} else if strings.EqualFold(incident.Status, "resolved") && incident.UpdatedAt != nil {
			end = *incident.UpdatedAt
		}
		for index := range days {
			dayStart, _ := time.Parse("2006-01-02", days[index].Date)
			if start.Before(dayStart.Add(24*time.Hour)) && end.After(dayStart) {
				days[index].Status = worseStatus(days[index].Status, incidentImpactStatus(incident.Impact))
				days[index].Incidents = append(days[index].Incidents, PublicServiceStatusBlip{ID: incident.ID, Name: incident.Name, Status: incident.Status, Impact: incident.Impact})
			}
		}
	}
	return days
}
func incidentAffectsComponents(incident PublicServiceStatusIncident, allowed map[string]bool) bool {
	if len(incident.Components) == 0 {
		return true
	}
	for _, component := range incident.Components {
		if allowed[component.ID] {
			return true
		}
	}
	return false
}
func incidentImpactStatus(impact string) string {
	switch strings.ToLower(impact) {
	case "critical":
		return "major_outage"
	case "major":
		return "partial_outage"
	case "minor":
		return "degraded_performance"
	case "maintenance":
		return "under_maintenance"
	default:
		return "operational"
	}
}
func worseStatus(left, right string) string {
	rank := map[string]int{"operational": 0, "under_maintenance": 1, "degraded_performance": 2, "partial_outage": 3, "major_outage": 4, "full_outage": 4}
	if rank[right] > rank[left] {
		return right
	}
	return left
}
func historyUptime(history []PublicServiceStatusDay) float64 {
	if len(history) == 0 {
		return 0
	}
	normal := 0
	for _, day := range history {
		if day.Status == "operational" {
			normal++
		}
	}
	return float64(normal) * 100 / float64(len(history))
}

func fetchStatusPage(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GatewayOps service status monitor")
	res, err := (&http.Client{Timeout: statusFetchTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status page returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, statusMaxBodyBytes)).Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty status page response")
		}
		return fmt.Errorf("decode status page response: %w", err)
	}
	return nil
}
