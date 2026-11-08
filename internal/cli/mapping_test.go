package cli

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
)

func marshalUTCTime(t *testing.T, utc output.UTCTime) string {
	t.Helper()
	data, err := json.Marshal(utc)
	if err != nil {
		t.Fatalf("unable to marshal UTCTime: %v", err)
	}

	return string(data)
}

func TestMapDBEntry_RunningEntry(t *testing.T) {
	description := "writing tests"

	tests := []struct {
		name        string
		description *string
		startUTC    time.Time
		wantStart   string
	}{
		{"with_description", &description, time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC), `"2026-01-01T09:30:15Z"`},
		{"without_description", nil, time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC), `"2026-01-01T09:30:15Z"`},
		{"nanoseconds_in_start", &description, time.Date(2026, 1, 1, 9, 30, 15, 123456789, time.UTC), `"2026-01-01T09:30:15Z"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := store.Entry{
				ID:          "0605115f-a394-4b6d-a8f7-528408bbc44f",
				Description: tc.description,
				StartUTC:    tc.startUTC,
				SyncState:   "pending",
				UpdatedAt:   tc.startUTC,
			}

			got := mapDBEntry(e)

			if got.ID != e.ID {
				t.Errorf("ID want %q, got %q", e.ID, got.ID)
			}

			if start := marshalUTCTime(t, got.Start); start != tc.wantStart {
				t.Errorf("Start want %s, got %s", tc.wantStart, start)
			}

			if tc.description == nil {
				if got.Description != nil {
					t.Errorf("Description want nil, got %q", *got.Description)
				}
			} else if got.Description == nil || *got.Description != *tc.description {
				t.Errorf("Description want %q, got %v", *tc.description, got.Description)
			}

			if got.SyncState != e.SyncState {
				t.Errorf("SyncState want %q, got %q", e.SyncState, got.SyncState)
			}

			// A running entry has no end, so nothing derived from it is set
			if got.End != nil {
				t.Errorf("End want nil, got %v", got.End)
			}
			if got.Seconds != nil {
				t.Errorf("Seconds want nil, got %d", *got.Seconds)
			}
			if got.Minutes != nil {
				t.Errorf("Minutes want nil, got %d", *got.Minutes)
			}

			if got.Task != nil {
				t.Errorf("Task want nil, got %+v", *got.Task)
			}
			if got.SyncError != nil {
				t.Errorf("SyncError want nil, got %q", *got.SyncError)
			}
			if got.Deleted {
				t.Errorf("Deleted want false, got true")
			}
		})
	}
}

func TestMapDBEntry_StoppedEntry(t *testing.T) {
	// Seconds and minutes are separate measures, not parts of one duration: seconds is the
	// real length, and minutes is the difference of the start and end truncated to the
	// minute, so 09:01:50 to 09:02:10 is 20 seconds and 1 minute (DESIGN.md §5)
	tests := []struct {
		name        string
		startUTC    time.Time
		endUTC      time.Time
		wantEnd     string
		wantSeconds int
		wantMinutes int
	}{
		{"whole_minutes", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 12, 0, 0, time.UTC), `"2026-01-01T10:12:00Z"`, 4320, 72},
		{"short_span_across_minute", time.Date(2026, 1, 1, 9, 1, 50, 0, time.UTC), time.Date(2026, 1, 1, 9, 2, 10, 0, time.UTC), `"2026-01-01T09:02:10Z"`, 20, 1},
		{"zero_minutes_within_minute", time.Date(2026, 1, 1, 9, 1, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 1, 59, 0, time.UTC), `"2026-01-01T09:01:59Z"`, 59, 0},
		{"ninety_seconds", time.Date(2026, 1, 1, 9, 0, 30, 0, time.UTC), time.Date(2026, 1, 1, 9, 2, 0, 0, time.UTC), `"2026-01-01T09:02:00Z"`, 90, 2},
		{"cross_midnight", time.Date(2026, 1, 1, 23, 59, 30, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 30, 0, time.UTC), `"2026-01-02T00:00:30Z"`, 60, 1},
		// Seconds are rounded to the nearest second, half away from zero
		{"seconds_round_down", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 400000000, time.UTC), `"2026-01-01T09:00:20Z"`, 20, 0},
		{"seconds_round_half_up", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 500000000, time.UTC), `"2026-01-01T09:00:20Z"`, 21, 0},
		{"seconds_round_up", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 700000000, time.UTC), `"2026-01-01T09:00:20Z"`, 21, 0},
		{"seconds_round_with_fractional_start", time.Date(2026, 1, 1, 9, 0, 0, 800000000, time.UTC), time.Date(2026, 1, 1, 9, 0, 21, 100000000, time.UTC), `"2026-01-01T09:00:21Z"`, 20, 0},
		{"under_half_second_rounds_to_zero", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 0, 400000000, time.UTC), `"2026-01-01T09:00:00Z"`, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			end := tc.endUTC
			e := store.Entry{
				ID:        "0605115f-a394-4b6d-a8f7-528408bbc44f",
				StartUTC:  tc.startUTC,
				EndUTC:    &end,
				SyncState: "pending",
				UpdatedAt: tc.endUTC,
			}

			got := mapDBEntry(e)

			if got.End == nil {
				t.Errorf("End want %s, got nil", tc.wantEnd)
			} else if gotEnd := marshalUTCTime(t, *got.End); gotEnd != tc.wantEnd {
				t.Errorf("End want %s, got %s", tc.wantEnd, gotEnd)
			}

			if got.Seconds == nil {
				t.Errorf("Seconds want %d, got nil", tc.wantSeconds)
			} else if *got.Seconds != tc.wantSeconds {
				t.Errorf("Seconds want %d, got %d", tc.wantSeconds, *got.Seconds)
			}

			// Minutes come from the start and end truncated to the minute (DESIGN.md §5),
			// so 0 is a real value for a stopped entry, not nil
			if got.Minutes == nil {
				t.Errorf("Minutes want %d, got nil", tc.wantMinutes)
			} else if *got.Minutes != tc.wantMinutes {
				t.Errorf("Minutes want %d, got %d", tc.wantMinutes, *got.Minutes)
			}
		})
	}
}

func TestMapNullableDBEntry(t *testing.T) {
	description := "writing tests"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		entry *store.Entry
	}{
		{"nil", nil},
		{"running", &store.Entry{ID: "0605115f-a394-4b6d-a8f7-528408bbc44f", Description: &description, StartUTC: start, SyncState: "pending", UpdatedAt: start}},
		{"stopped", &store.Entry{ID: "0605115f-a394-4b6d-a8f7-528408bbc44f", Description: &description, StartUTC: start, EndUTC: &end, SyncState: "pending", UpdatedAt: end}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapNullableDBEntry(tc.entry)

			if tc.entry == nil {
				if got != nil {
					t.Errorf("want nil, got %+v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want an entry, got nil")
			}
			// A non-nil entry maps exactly as mapDBEntry maps it
			if want := mapDBEntry(*tc.entry); !reflect.DeepEqual(*got, want) {
				t.Errorf("want %+v, got %+v", want, *got)
			}
		})
	}
}

func TestMapDBStatus_NothingRunning(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	end1 := time.Date(2026, 1, 1, 10, 12, 0, 0, time.UTC)
	end2 := time.Date(2026, 1, 1, 11, 2, 10, 0, time.UTC)

	s := store.Status{
		Connected: true,
		TodayEntries: []store.Entry{
			{ID: "a", StartUTC: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), EndUTC: &end1, SyncState: "pending"},
			// 20 seconds across a minute boundary counts as 1 minute (DESIGN.md §5)
			{ID: "b", StartUTC: time.Date(2026, 1, 1, 11, 1, 50, 0, time.UTC), EndUTC: &end2, SyncState: "pending"},
		},
		CountSummary: store.CountSummary{Pending: 1, Blocked: 2, Waiting: 3},
	}

	got := mapDBStatus(s, now)

	if got.Running != nil {
		t.Errorf("Running want nil, got %+v", *got.Running)
	}
	if got.ElapsedSeconds != nil {
		t.Errorf("ElapsedSeconds want nil, got %d", *got.ElapsedSeconds)
	}
	if want := (output.TodayCounts{Entries: 2, Minutes: 73}); got.Today != want {
		t.Errorf("Today want %+v, got %+v", want, got.Today)
	}
	if want := (output.CountsSummary{Pending: 1, Blocked: 2, Waiting: 3}); got.Counts != want {
		t.Errorf("Counts want %+v, got %+v", want, got.Counts)
	}
	if !got.Connected {
		t.Errorf("Connected want true, got false")
	}
	if got.Warnings == nil || len(got.Warnings) != 0 {
		t.Errorf("Warnings want an empty slice, got %v", got.Warnings)
	}
}

func TestMapDBStatus_Running(t *testing.T) {
	const runningID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	stoppedEnd := time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC)
	stopped := store.Entry{ID: "a", StartUTC: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC), EndUTC: &stoppedEnd, SyncState: "pending"}

	// The store returns a running entry that started today in TodayEntries as well, and one
	// from yesterday only as Running
	tests := []struct {
		name         string
		start        time.Time
		now          time.Time
		startedToday bool
		wantElapsed  int
		wantToday    output.TodayCounts
	}{
		// 30 minutes stopped plus 90 minutes running
		{"started_today", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC), true, 5400, output.TodayCounts{Entries: 2, Minutes: 120}},
		// The running entry's minutes are truncated like a stopped entry's, up to now
		{"running_minutes_truncated", time.Date(2026, 1, 1, 9, 1, 50, 0, time.UTC), time.Date(2026, 1, 1, 9, 2, 10, 0, time.UTC), true, 20, output.TodayCounts{Entries: 2, Minutes: 31}},
		// Elapsed seconds are rounded to the nearest second, half away from zero
		{"elapsed_rounds_half_up", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 500000000, time.UTC), true, 21, output.TodayCounts{Entries: 2, Minutes: 30}},
		{"elapsed_rounds_down", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 400000000, time.UTC), true, 20, output.TodayCounts{Entries: 2, Minutes: 30}},
		// A timer left running from yesterday isn't part of today (DESIGN.md §8)
		{"started_yesterday", time.Date(2025, 12, 31, 22, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), false, 43200, output.TodayCounts{Entries: 1, Minutes: 30}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			running := store.Entry{ID: runningID, StartUTC: tc.start, SyncState: "pending", UpdatedAt: tc.start}
			today := []store.Entry{stopped}
			if tc.startedToday {
				today = append(today, running)
			}
			s := store.Status{Running: &running, TodayEntries: today}

			got := mapDBStatus(s, tc.now)

			if got.Running == nil {
				t.Fatalf("Running want entry %q, got nil", runningID)
			}
			if got.Running.ID != runningID {
				t.Errorf("Running.ID want %q, got %q", runningID, got.Running.ID)
			}
			if got.Running.End != nil {
				t.Errorf("Running.End want nil, got %v", got.Running.End)
			}
			if got.ElapsedSeconds == nil {
				t.Errorf("ElapsedSeconds want %d, got nil", tc.wantElapsed)
			} else if *got.ElapsedSeconds != tc.wantElapsed {
				t.Errorf("ElapsedSeconds want %d, got %d", tc.wantElapsed, *got.ElapsedSeconds)
			}
			if got.Today != tc.wantToday {
				t.Errorf("Today want %+v, got %+v", tc.wantToday, got.Today)
			}
		})
	}
}

func TestRunningEntryWarnings(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	running := &store.Entry{ID: "0605115f-a394-4b6d-a8f7-528408bbc44f", StartUTC: start, SyncState: "pending", UpdatedAt: start}

	// The stale-timer threshold is fixed at 12h in v1, and only a timer running over it warns (DESIGN.md §11.3)
	tests := []struct {
		name      string
		running   *store.Entry
		now       time.Time
		wantStale bool
	}{
		{"nothing_running", nil, start.Add(24 * time.Hour), false},
		{"under_twelve_hours", running, start.Add(11*time.Hour + 59*time.Minute), false},
		{"exactly_twelve_hours", running, start.Add(12 * time.Hour), false},
		{"just_over_twelve_hours", running, start.Add(12*time.Hour + time.Nanosecond), true},
		{"started_yesterday", running, start.Add(25 * time.Hour), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runningEntryWarnings(tc.running, tc.now)

			// warnings is always an array in JSON, so never nil (DESIGN.md §8)
			if got == nil {
				t.Fatalf("want a non-nil slice, got nil")
			}
			if !tc.wantStale {
				if len(got) != 0 {
					t.Errorf("want no warnings, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want 1 warning, got %d: %+v", len(got), got)
			}
			if got[0].Code != output.StaleTimer {
				t.Errorf("Code want %q, got %q", output.StaleTimer, got[0].Code)
			}
			if got[0].Details != nil {
				t.Errorf("Details want nil, got %v", got[0].Details)
			}
		})
	}
}

func TestMapDbTasks(t *testing.T) {
	order := 4.1
	inputs := []store.Task{
		{ContextID: "ctx", PageID: "3f63b23e837922", Name: "Write the sync engine", SortOrder: &order, ProjectPageID: ptr("project1")},
		{ContextID: "ctx", PageID: "4a74c34fa1b2c3", Name: "Plan", Done: true},
		{ContextID: "ctx", PageID: "5b85d45fnotmapped", Name: "Missing from the map"},
	}
	shortIDs := map[string]string{
		"3f63b23e837922": "837922",
		"4a74c34fa1b2c3": "a1b2c3",
	}

	doc := mapDbTasks(inputs, shortIDs, false, nil)

	// The tasks keep the order they came from the store in
	want := []output.TaskItem{
		{PageID: "3f63b23e837922", ShortID: "837922", Order: &order, Name: "Write the sync engine"},
		{PageID: "4a74c34fa1b2c3", ShortID: "a1b2c3", Name: "Plan", Done: true},
		// A page ID with no short ID falls back to the full page ID
		{PageID: "5b85d45fnotmapped", ShortID: "5b85d45fnotmapped", Name: "Missing from the map"},
	}
	if !reflect.DeepEqual(doc.Tasks, want) {
		t.Errorf("Tasks = %+v, want %+v", doc.Tasks, want)
	}

	if doc.SchemaVersion != output.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", doc.SchemaVersion, output.SchemaVersion)
	}
	if doc.Warnings == nil {
		t.Error("Warnings = nil, want an empty slice")
	}
}

func TestMapDbTasks_Truncated(t *testing.T) {
	inputs := []store.Task{{PageID: "3f63b23e837922", Name: "Write the sync engine"}}

	for _, truncated := range []bool{false, true} {
		doc := mapDbTasks(inputs, map[string]string{}, truncated, nil)

		if doc.Truncated != truncated {
			t.Errorf("mapDbTasks(truncated=%v) Truncated = %v", truncated, doc.Truncated)
		}
	}
}

func TestMapDbTasks_NoTasks(t *testing.T) {
	for _, inputs := range [][]store.Task{nil, {}} {
		doc := mapDbTasks(inputs, map[string]string{}, false, nil)

		// tasks is always an array in JSON, so never nil (DESIGN.md §8)
		if doc.Tasks == nil || len(doc.Tasks) != 0 {
			t.Errorf("mapDbTasks(%v) Tasks = %v, want an empty slice", inputs, doc.Tasks)
		}
	}
}

func TestMapDbTasks_Warnings(t *testing.T) {
	refreshFailed := output.Warning{Code: output.TaskCacheStale, Message: "refresh failed, using cached tasks"}
	refreshSkipped := output.Warning{Code: output.TaskCacheStale, Message: "refresh skipped, using cached tasks"}

	tests := []struct {
		name     string
		warnings []output.Warning
		want     []output.Warning
	}{
		// warnings is always an array in JSON, so never nil (DESIGN.md §8)
		{name: "nil", warnings: nil, want: []output.Warning{}},
		{name: "empty", warnings: []output.Warning{}, want: []output.Warning{}},
		{name: "one warning", warnings: []output.Warning{refreshFailed}, want: []output.Warning{refreshFailed}},
		{name: "several keep their order", warnings: []output.Warning{refreshSkipped, refreshFailed}, want: []output.Warning{refreshSkipped, refreshFailed}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []store.Task{{PageID: "3f63b23e837922", Name: "Write tests"}}

			doc := mapDbTasks(inputs, map[string]string{}, false, tc.warnings)

			if !reflect.DeepEqual(doc.Warnings, tc.want) {
				t.Errorf("Warnings = %#v, want %#v", doc.Warnings, tc.want)
			}
		})
	}
}

func TestMapDbTasks_Project(t *testing.T) {
	tests := []struct {
		name          string
		projectPageID *string
		projectName   *string
		want          *output.ProjectRef
	}{
		{
			name:          "cached project",
			projectPageID: ptr("project1"),
			projectName:   ptr("Sabbatical"),
			want:          &output.ProjectRef{PageID: "project1", Name: "Sabbatical"},
		},
		{
			name: "no project",
			want: nil,
		},
		{
			// The store gives no name when the project isn't cached or is gone
			name:          "project without a name",
			projectPageID: ptr("project1"),
			want:          nil,
		},
		// Edge cases
		{
			name:          "untitled project",
			projectPageID: ptr("project1"),
			projectName:   ptr(""),
			want:          &output.ProjectRef{PageID: "project1", Name: ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []store.Task{{PageID: "3f63b23e837922", Name: "Write tests", ProjectPageID: tc.projectPageID, ProjectName: tc.projectName}}

			doc := mapDbTasks(inputs, map[string]string{}, false, nil)

			if len(doc.Tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(doc.Tasks))
			}
			if got := doc.Tasks[0].Project; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Project = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMapDBEntryWithShortTask(t *testing.T) {
	const (
		entryID    = "0605115f-a394-4b6d-a8f7-528408bbc44f"
		taskPageID = "3f63b23e837922"
	)

	tests := []struct {
		name       string
		taskPageID *string
		taskName   *string
		shortIDMap map[string]string
		want       *output.TaskRef
	}{
		{
			name:       "task with a short ID",
			taskPageID: ptr(taskPageID),
			taskName:   ptr("Write the sync engine"),
			shortIDMap: map[string]string{taskPageID: "837922", "4a74c34fa1b2c3": "a1b2c3"},
			want:       &output.TaskRef{PageID: taskPageID, ShortID: "837922", Name: "Write the sync engine"},
		},
		{
			name:       "task missing from the map",
			taskPageID: ptr(taskPageID),
			taskName:   ptr("Write the sync engine"),
			shortIDMap: map[string]string{"4a74c34fa1b2c3": "a1b2c3"},
			want:       &output.TaskRef{PageID: taskPageID, Name: "Write the sync engine"},
		},
		{
			name:       "empty map",
			taskPageID: ptr(taskPageID),
			taskName:   ptr("Write the sync engine"),
			shortIDMap: map[string]string{},
			want:       &output.TaskRef{PageID: taskPageID, Name: "Write the sync engine"},
		},
		{
			name:       "nil map",
			taskPageID: ptr(taskPageID),
			taskName:   ptr("Write the sync engine"),
			shortIDMap: nil,
			want:       &output.TaskRef{PageID: taskPageID, Name: "Write the sync engine"},
		},
		{
			// The map is keyed by task page ID, so a match on the entry ID is not the task's short ID
			name:       "map keyed by the entry ID only",
			taskPageID: ptr(taskPageID),
			taskName:   ptr("Write the sync engine"),
			shortIDMap: map[string]string{entryID: "wrong1"},
			want:       &output.TaskRef{PageID: taskPageID, Name: "Write the sync engine"},
		},
		{
			name:       "no task",
			shortIDMap: map[string]string{taskPageID: "837922"},
			want:       nil,
		},
		{
			// The store gives no name when the task isn't cached
			name:       "task without a name",
			taskPageID: ptr(taskPageID),
			shortIDMap: map[string]string{taskPageID: "837922"},
			want:       nil,
		},
		// Edge cases
		{
			name:       "untitled task",
			taskPageID: ptr(taskPageID),
			taskName:   ptr(""),
			shortIDMap: map[string]string{taskPageID: "837922"},
			want:       &output.TaskRef{PageID: taskPageID, ShortID: "837922", Name: ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
			end := time.Date(2026, 1, 1, 10, 12, 0, 0, time.UTC)
			e := store.Entry{
				ID:         entryID,
				StartUTC:   start,
				EndUTC:     &end,
				TaskPageID: tc.taskPageID,
				TaskName:   tc.taskName,
				SyncState:  "pending",
				UpdatedAt:  end,
			}

			got := mapDBEntryWithShortTask(e, tc.shortIDMap)

			if !reflect.DeepEqual(got.Task, tc.want) {
				t.Errorf("Task = %+v, want %+v", got.Task, tc.want)
			}

			// Everything other than the task is mapped as mapDBEntry maps it
			want := mapDBEntry(e)
			got.Task, want.Task = nil, nil
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mapDBEntryWithShortTask() = %+v, want %+v", got, want)
			}
		})
	}
}
