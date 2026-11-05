package taskcache

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
)

const taskRefreshOverlapMinutes = 5

type notionPageFetcher interface {
	QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]notion.Page, error)
}

type clock interface {
	NowUTC() time.Time
}

type RefreshResult struct {
	RefreshedAt  time.Time
	FetchedCount int
	GoneCount    *int
}

// AutoRefresh refreshes cached tasks if the last refresh was more than 10 minutes ago,
// and is allowed to run for no more than 2 seconds. If this timeout is exceeded or any
// other failure is encountered, further refresh attempts are blocked for 1 minute
func AutoRefresh(ctx context.Context, c clock, s *store.Store, n notionPageFetcher, tasksDataSourceID, projectsDataSourceID string) (RefreshResult, error) {
	nowUTC := c.NowUTC()

	cacheState, err := s.CacheState(ctx)
	if err != nil {
		return RefreshResult{}, err
	}

	// 1 minute backoff before retrying refresh
	if cacheState.TasksRefreshFailedAt != nil && nowUTC.Sub(*cacheState.TasksRefreshFailedAt) < time.Minute {
		return RefreshResult{}, nil
	}

	// Don't refresh if last refresh less than 10 minutes ago, no error
	if cacheState.TasksRefreshedAt != nil && nowUTC.Sub(*cacheState.TasksRefreshedAt) < (10*time.Minute) {
		return RefreshResult{}, nil
	}

	// Incremental refresh (full = false), capped at 2 seconds
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return Refresh(ctx, c, s, n, tasksDataSourceID, projectsDataSourceID, false)
}

func Refresh(ctx context.Context, c clock, s *store.Store, n notionPageFetcher, tasksDataSourceID, projectsDataSourceID string, full bool) (result RefreshResult, err error) {
	nowUTC := c.NowUTC()

	// Fetch last refresh from DB
	cacheState, err := s.CacheState(ctx)
	if err != nil {
		return RefreshResult{}, err
	}

	// Ensure any timeout/error refresh failures are recorded
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) {
			noCancelCtx := context.WithoutCancel(ctx)
			_ = s.RecordTasksRefreshFailed(noCancelCtx, nowUTC)
		}
	}()

	projects, err := fetchNotionProjects(ctx, n, projectsDataSourceID)
	if err != nil {
		return RefreshResult{}, err
	}

	tasks, err := fetchNotionTasks(ctx, n, tasksDataSourceID, cacheState.TasksRefreshedAt, full)
	if err != nil {
		return RefreshResult{}, err
	}

	// Save projects and tasks to DB
	_, err = s.RefreshProjects(ctx, projects, nowUTC)
	if err != nil {
		return RefreshResult{}, err
	}

	goneCount, err := s.RefreshTasks(ctx, tasks, nowUTC, full)
	if err != nil {
		return RefreshResult{}, err
	}

	return RefreshResult{
		RefreshedAt:  nowUTC,
		FetchedCount: len(tasks),
		GoneCount:    goneCount,
	}, nil
}

func fetchNotionProjects(ctx context.Context, n notionPageFetcher, dataSourceID string) ([]store.Project, error) {
	projPages, err := n.QueryDataSource(ctx, dataSourceID, nil)
	if err != nil {
		return nil, err
	}

	projects := make([]store.Project, len(projPages))
	for i, p := range projPages {
		projects[i] = mapPageToProject(p, store.DefaultContextID)
	}

	return projects, nil
}

func fetchNotionTasks(ctx context.Context, n notionPageFetcher, dataSourceID string, tasksLastRefreshed *time.Time, full bool) ([]store.Task, error) {
	// Fetch tasks since last refresh
	var sinceTime *time.Time
	if tasksLastRefreshed != nil && !full {
		adjusted := tasksLastRefreshed.Add(-taskRefreshOverlapMinutes * time.Minute)
		sinceTime = &adjusted
	}
	pages, err := n.QueryDataSource(ctx, dataSourceID, sinceTime)
	if err != nil {
		return nil, err
	}

	tasks := make([]store.Task, len(pages))
	for i, p := range pages {
		tasks[i] = mapPageToTask(p, store.DefaultContextID)
	}

	return tasks, nil
}

func mapPageToTask(p notion.Page, contextID string) store.Task {
	var name string
	if prop, ok := p.Properties["Task"]; ok && prop.Type == "title" {
		var sb strings.Builder
		for _, t := range prop.Title {
			sb.WriteString(t.PlainText)
		}
		name = sb.String()
	}

	var projectID *string
	if prop, ok := p.Properties["Project"]; ok && prop.Type == "relation" {
		if len(prop.Relation) > 0 {
			sanitisedProjectID := store.SanitiseID(prop.Relation[0].ID)
			projectID = &sanitisedProjectID
		}
	}

	var orderNum *float64
	if prop, ok := p.Properties["Order"]; ok && prop.Type == "number" {
		orderNum = prop.Number
	}

	var done bool
	if prop, ok := p.Properties["Done"]; ok && prop.Type == "checkbox" {
		done = prop.Checkbox
	}

	return store.Task{
		ContextID:      contextID,
		PageID:         store.SanitiseID(p.ID),
		Name:           name,
		ProjectPageID:  projectID,
		SortOrder:      orderNum,
		Done:           done,
		LastEditedTime: p.LastEditedTimeUTC,
	}
}

func mapPageToProject(p notion.Page, contextID string) store.Project {
	var name string
	if prop, ok := p.Properties["Project name"]; ok && prop.Type == "title" {
		var sb strings.Builder
		for _, t := range prop.Title {
			sb.WriteString(t.PlainText)
		}
		name = sb.String()
	}

	return store.Project{
		ContextID: contextID,
		PageID:    store.SanitiseID(p.ID),
		Name:      name,
	}
}
