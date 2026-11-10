package notion

import (
	"context"
	"net/http"
)

func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	type user struct {
		Name string `json:"name"`
	}

	var usr user
	if err := c.do(ctx, http.MethodGet, "/users/me", nil, &usr); err != nil {
		return "", err
	}

	return usr.Name, nil
}
