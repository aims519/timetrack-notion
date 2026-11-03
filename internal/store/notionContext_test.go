package store

import (
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// connectedContext has every field set, with values that differ from each other
// so a swapped column is caught
func connectedContext() NotionContext {
	return NotionContext{
		ID:                   testContextID,
		TasksDataSourceID:    ptr("tasks-ds"),
		ProjectsDataSourceID: ptr("projects-ds"),
		EntriesDataSourceID:  ptr("entries-ds"),
		PropertyMap: map[string]string{
			"name":    "Task",
			"project": "Project",
			"order":   "Order",
			"done":    "Done",
		},
		DefaultProjectPageID: ptr("default-project"),
	}
}

func TestNotionContext_FreshDatabase_NotConnected(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	// Every connection column is NULL until init, and NULL must read back without an error
	got, err := st.NotionContext(t.Context(), testContextID)
	if err != nil {
		t.Fatalf("NotionContext() want nil error, got %v", err)
	}

	checkNotionContext(t, got, NotionContext{ID: testContextID})
}

func TestSaveNotionContext_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		save NotionContext
		want NotionContext
	}{
		{
			name: "every field set",
			save: connectedContext(),
			want: connectedContext(),
		},
		{
			name: "every nullable field null",
			save: NotionContext{ID: testContextID},
			want: NotionContext{ID: testContextID},
		},
		{
			// Null entries data source means local-only (DESIGN.md §4), and no default project is allowed
			name: "local-only with no default project",
			save: func() NotionContext {
				c := connectedContext()
				c.EntriesDataSourceID = nil
				c.DefaultProjectPageID = nil
				return c
			}(),
			want: func() NotionContext {
				c := connectedContext()
				c.EntriesDataSourceID = nil
				c.DefaultProjectPageID = nil
				return c
			}(),
		},
		// Edge cases
		{
			// An empty map isn't nil, so it's stored as "{}" and reads back empty, not nil
			name: "empty property map",
			save: NotionContext{ID: testContextID, PropertyMap: map[string]string{}},
			want: NotionContext{ID: testContextID, PropertyMap: map[string]string{}},
		},
		{
			name: "property names with quotes and non-ASCII characters",
			save: NotionContext{ID: testContextID, PropertyMap: map[string]string{"name": `Task "title"`, "done": "Fertig ✓"}},
			want: NotionContext{ID: testContextID, PropertyMap: map[string]string{"name": `Task "title"`, "done": "Fertig ✓"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			if err := st.SaveNotionContext(t.Context(), tc.save); err != nil {
				t.Fatalf("SaveNotionContext() want nil error, got %v", err)
			}

			got, err := st.NotionContext(t.Context(), testContextID)
			if err != nil {
				t.Fatalf("NotionContext() want nil error, got %v", err)
			}

			checkNotionContext(t, got, tc.want)
		})
	}
}

func TestSaveNotionContext_OverwritesEarlierValues(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	if err := st.SaveNotionContext(t.Context(), connectedContext()); err != nil {
		t.Fatalf("first SaveNotionContext() want nil error, got %v", err)
	}

	// Re-running init saves everything again: values change, and some go back to null
	second := NotionContext{
		ID:                   testContextID,
		TasksDataSourceID:    ptr("new-tasks-ds"),
		ProjectsDataSourceID: ptr("new-projects-ds"),
		EntriesDataSourceID:  nil,
		PropertyMap:          map[string]string{"name": "Name"},
		DefaultProjectPageID: nil,
	}
	if err := st.SaveNotionContext(t.Context(), second); err != nil {
		t.Fatalf("second SaveNotionContext() want nil error, got %v", err)
	}

	got, err := st.NotionContext(t.Context(), testContextID)
	if err != nil {
		t.Fatalf("NotionContext() want nil error, got %v", err)
	}

	// The whole map is replaced, so keys only in the first map are gone
	checkNotionContext(t, got, second)
}

func TestSaveNotionContext_PropertyMapStoredAsJSON(t *testing.T) {
	tests := []struct {
		name        string
		propertyMap map[string]string
		wantNull    bool
	}{
		{"map set", connectedContext().PropertyMap, false},
		{"nil map is null", nil, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			if err := st.SaveNotionContext(t.Context(), NotionContext{ID: testContextID, PropertyMap: tc.propertyMap}); err != nil {
				t.Fatalf("SaveNotionContext() want nil error, got %v", err)
			}

			var raw *string
			err := st.connectionPool.QueryRowContext(t.Context(),
				`SELECT property_map FROM contexts WHERE id = ?`, testContextID).Scan(&raw)
			if err != nil {
				t.Fatalf("reading property_map failed: %v", err)
			}

			if tc.wantNull {
				if raw != nil {
					t.Errorf("property_map = %q, want NULL", *raw)
				}
				return
			}

			if raw == nil {
				t.Fatal("property_map = NULL, want a JSON object")
			}

			// Decoding the raw column checks it's a JSON object with the right keys and values,
			// whatever order or spacing it was written in
			var got map[string]string
			if err := json.Unmarshal([]byte(*raw), &got); err != nil {
				t.Fatalf("property_map %q isn't a JSON object of strings: %v", *raw, err)
			}
			if !maps.Equal(got, tc.propertyMap) {
				t.Errorf("property_map decodes to %v, want %v", got, tc.propertyMap)
			}
		})
	}
}

func TestSaveNotionContext_MissingContext(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	missing := connectedContext()
	missing.ID = "11111111-1111-1111-1111-111111111111"

	err := st.SaveNotionContext(t.Context(), missing)
	if err == nil {
		t.Fatal("SaveNotionContext() want error for a context that doesn't exist, got nil")
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}
	if ce.Code != clierr.DatabaseUnusable {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.DatabaseUnusable)
	}

	// The context that does exist is untouched
	got, err := st.NotionContext(t.Context(), testContextID)
	if err != nil {
		t.Fatalf("NotionContext() want nil error, got %v", err)
	}
	checkNotionContext(t, got, NotionContext{ID: testContextID})
}

func TestNotionContext_Errors(t *testing.T) {
	tests := []struct {
		name        string
		contextID   string
		propertyMap any // written straight to the column before reading
	}{
		{name: "context doesn't exist", contextID: "11111111-1111-1111-1111-111111111111"},
		{name: "property_map isn't JSON", contextID: testContextID, propertyMap: "not json"},
		{name: "property_map isn't an object of strings", contextID: testContextID, propertyMap: `{"order": 4}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(),
				`UPDATE contexts SET property_map = ? WHERE id = ?`, tc.propertyMap, testContextID)
			if err != nil {
				t.Fatalf("update property_map failed: %v", err)
			}

			got, err := st.NotionContext(t.Context(), tc.contextID)
			if err == nil {
				t.Fatalf("NotionContext() want error, got nil and %+v", got)
			}

			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}
			if ce.Code != clierr.DatabaseUnusable {
				t.Errorf("error code = %q, want %q", ce.Code, clierr.DatabaseUnusable)
			}
		})
	}
}

// checkNotionContext compares every field, telling a nil pointer or map apart from an empty one
func checkNotionContext(t *testing.T, got, want NotionContext) {
	t.Helper()

	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}

	checkStringPtr(t, "TasksDataSourceID", got.TasksDataSourceID, want.TasksDataSourceID)
	checkStringPtr(t, "ProjectsDataSourceID", got.ProjectsDataSourceID, want.ProjectsDataSourceID)
	checkStringPtr(t, "EntriesDataSourceID", got.EntriesDataSourceID, want.EntriesDataSourceID)
	checkStringPtr(t, "DefaultProjectPageID", got.DefaultProjectPageID, want.DefaultProjectPageID)

	switch {
	case want.PropertyMap == nil && got.PropertyMap != nil:
		t.Errorf("PropertyMap = %v, want nil", got.PropertyMap)
	case want.PropertyMap != nil && got.PropertyMap == nil:
		t.Errorf("PropertyMap = nil, want %v", want.PropertyMap)
	case !maps.Equal(got.PropertyMap, want.PropertyMap):
		t.Errorf("PropertyMap = %v, want %v", got.PropertyMap, want.PropertyMap)
	}
}

func checkStringPtr(t *testing.T, field string, got, want *string) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %q, want nil", field, *got)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %q", field, *want)
	case want != nil && *got != *want:
		t.Errorf("%s = %q, want %q", field, *got, *want)
	}
}
