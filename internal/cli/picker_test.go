package cli

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
)

const (
	pickerOpenID    = "7777777777777777777777777a111111"
	pickerDoneID    = "7777777777777777777777777b222222"
	pickerOtherID   = "7777777777777777777777777c333333"
	pickerProjectID = "8888888888888888888888888d444444"
)

// seedPickerTasks caches the given tasks in the default context
func seedPickerTasks(t *testing.T, dbPath string, tasks []store.Task) {
	t.Helper()

	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	for i := range tasks {
		tasks[i].ContextID = store.DefaultContextID
		tasks[i].LastEditedTime = edited
	}
	onStore(t, dbPath, func(st *store.Store) {
		if _, err := st.RefreshTasks(t.Context(), tasks, edited, true); err != nil {
			t.Fatalf("RefreshTasks(): %v", err)
		}
	})
}

// pickerTasks is an open and a done task in the project, and an open task with no project
func pickerTasks() []store.Task {
	return []store.Task{
		{PageID: pickerOpenID, Name: "Write tests", ProjectPageID: ptr(pickerProjectID), SortOrder: ptr(1.0)},
		{PageID: pickerDoneID, Name: "Ship it", ProjectPageID: ptr(pickerProjectID), SortOrder: ptr(2.0), Done: true},
		{PageID: pickerOtherID, Name: "Read docs"},
	}
}

// loadAllTasksAt runs loadAllTasks against the database at dbPath
func loadAllTasksAt(t *testing.T, dbPath string, all bool, commandName string) (tasks []output.TaskItem, shortIDMap map[string]string, err error) {
	t.Helper()

	onStore(t, dbPath, func(st *store.Store) {
		var mapErr error
		shortIDMap, mapErr = buildShortIDMap(t.Context(), st)
		if mapErr != nil {
			t.Fatalf("buildShortIDMap(): %v", mapErr)
		}
		tasks, err = loadAllTasks(t.Context(), st, all, shortIDMap, commandName)
	})

	return tasks, shortIDMap, err
}

// pageIDs lists the page IDs of the picker rows, in order
func pageIDs(tasks []output.TaskItem) []string {
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.PageID)
	}
	return ids
}

// The picker lists open tasks, and done tasks only with --all, in sort order (§3.1)
func TestLoadAllTasks_Tasks(t *testing.T) {
	cases := []struct {
		name           string
		all            bool
		defaultProject bool
		want           []string
	}{
		{name: "open_only", want: []string{pickerOpenID, pickerOtherID}},
		{name: "all_includes_done", all: true, want: []string{pickerOpenID, pickerDoneID, pickerOtherID}},
		{name: "default_project", defaultProject: true, want: []string{pickerOpenID}},
		{name: "default_project_all", all: true, defaultProject: true, want: []string{pickerOpenID, pickerDoneID}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedConnectedContext(t, dbPath)
			if tc.defaultProject {
				seedDefaultProject(t, dbPath, pickerProjectID)
			}
			seedPickerTasks(t, dbPath, pickerTasks())

			got, _, err := loadAllTasksAt(t, dbPath, tc.all, "attach")
			if err != nil {
				t.Fatalf("loadAllTasks(): %v", err)
			}
			if ids := pageIDs(got); !slices.Equal(ids, tc.want) {
				t.Errorf("page IDs want %v, got %v", tc.want, ids)
			}
		})
	}
}

// Picker rows carry each task's short ID, order and name, which make up the picker label
func TestLoadAllTasks_RowValues(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedConnectedContext(t, dbPath)
	seedPickerTasks(t, dbPath, pickerTasks())

	got, shortIDMap, err := loadAllTasksAt(t, dbPath, false, "attach")
	if err != nil {
		t.Fatalf("loadAllTasks(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tasks, got %d: %+v", len(got), got)
	}

	cases := []struct {
		name      string
		task      output.TaskItem
		wantID    string
		wantOrder *float64
		wantName  string
	}{
		{name: "with_order", task: got[0], wantID: pickerOpenID, wantOrder: ptr(1.0), wantName: "Write tests"},
		{name: "no_order", task: got[1], wantID: pickerOtherID, wantName: "Read docs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.task.PageID != tc.wantID {
				t.Errorf("PageID want %q, got %q", tc.wantID, tc.task.PageID)
			}
			if want := shortIDMap[tc.wantID]; tc.task.ShortID != want || want == "" {
				t.Errorf("ShortID want %q, got %q", want, tc.task.ShortID)
			}
			switch {
			case tc.wantOrder == nil && tc.task.Order != nil:
				t.Errorf("Order want nil, got %v", *tc.task.Order)
			case tc.wantOrder != nil && (tc.task.Order == nil || *tc.task.Order != *tc.wantOrder):
				t.Errorf("Order want %v, got %v", *tc.wantOrder, tc.task.Order)
			}
			if tc.task.Name != tc.wantName {
				t.Errorf("Name want %q, got %q", tc.wantName, tc.task.Name)
			}
		})
	}
}

// With nothing to pick, loadAllTasks fails with not_found before any picker opens. The --all
// hint only appears when --all wasn't given, and names the command that was run
func TestLoadAllTasks_NoTasks_IsNotFound(t *testing.T) {
	cases := []struct {
		name        string
		tasks       []store.Task
		all         bool
		commandName string
		wantAllHint bool
	}{
		{name: "empty_cache", commandName: "attach", wantAllHint: true},
		{name: "empty_cache_all", all: true, commandName: "attach"},
		{name: "only_done", tasks: []store.Task{{PageID: pickerDoneID, Name: "Ship it", Done: true}}, commandName: "start", wantAllHint: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedConnectedContext(t, dbPath)
			if tc.tasks != nil {
				seedPickerTasks(t, dbPath, tc.tasks)
			}

			got, _, err := loadAllTasksAt(t, dbPath, tc.all, tc.commandName)
			if got != nil {
				t.Errorf("tasks want nil, got %+v", got)
			}

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("want *clierr.Error, got %T: %v", err, err)
			}
			if cliErr.Code != clierr.NotFound {
				t.Errorf("Code want %q, got %q", clierr.NotFound, cliErr.Code)
			}
			if want := "no tasks available to " + tc.commandName; !strings.Contains(cliErr.Message, want) {
				t.Errorf("Message %q should contain %q", cliErr.Message, want)
			}
			if !strings.Contains(cliErr.Message, "tt task refresh") {
				t.Errorf("Message %q should suggest 'tt task refresh'", cliErr.Message)
			}
			hint := "--all in 'tt " + tc.commandName + "'"
			if gotHint := strings.Contains(cliErr.Message, hint); gotHint != tc.wantAllHint {
				t.Errorf("Message %q: contains %q want %v, got %v", cliErr.Message, hint, tc.wantAllHint, gotHint)
			}
		})
	}
}
