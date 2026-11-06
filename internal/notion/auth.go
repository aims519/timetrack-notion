package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/users/me", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}
	addDefaultHeaders(req, c.apiVersion, c.apiToken)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion", Cause: err}
	}
	defer res.Body.Close()

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return "", &clierr.Error{Code: clierr.Internal, Message: "unable to read response from Notion", Cause: err}
	}

	err = handleErrorResponse(res, bytes)
	if err != nil {
		return "", err
	}

	type user struct {
		Name string `json:"name"`
	}

	var usr user
	err = json.Unmarshal(bytes, &usr)
	if err != nil {
		return "", &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response", Cause: err}
	}

	return usr.Name, nil
}
