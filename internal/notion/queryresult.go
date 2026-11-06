package notion

type queryResult struct {
	PageType      string            `json:"type"`
	NextCursor    string            `json:"next_cursor"`
	HasMore       bool              `json:"has_more"`
	Results       []Page            `json:"results"`
	RequestStatus queryResultStatus `json:"request_status"`
}

type queryResultStatus struct {
	Type             string `json:"type"`
	IncompleteReason string `json:"incomplete_reason"`
}
