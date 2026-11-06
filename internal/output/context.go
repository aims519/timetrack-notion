package output

type NotionContext struct {
	ContextID            string            `json:"context_id"`
	TasksDataSourceID    *string           `json:"tasks_data_source_id"`
	ProjectsDataSourceID *string           `json:"projects_data_source_id"`
	EntriesDataSourceID  *string           `json:"entries_data_source_id"`
	DefaultProjectPageID *string           `json:"default_project_page_id"`
	PropertyMap          map[string]string `json:"property_map"`
}
