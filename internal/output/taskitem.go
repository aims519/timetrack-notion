package output

type TaskItem struct {
	PageID  string      `json:"page_id"`
	ShortID string      `json:"short_id"`
	Order   *float64    `json:"order"`
	Name    string      `json:"name"`
	Project *ProjectRef `json:"project"`
	Done    bool        `json:"done"`
}
