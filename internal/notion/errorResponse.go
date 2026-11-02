package notion

type errorResponse struct {
	Object  string `json:"object"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Status  int    `json:"status"`
}
