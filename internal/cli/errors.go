package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
)

// exitCode returns the process exit code for err, using the code of the first
// *clierr.Error in its chain, or the internal exit code if there is none.
func exitCode(err error) int {
	var cliError *clierr.Error
	if errors.As(err, &cliError) {
		return cliError.Code.ExitCode()
	}
	return clierr.Internal.ExitCode() // unknown internal error
}

// printError writes err to w in format f. An error that is not a *clierr.Error is
// reported as internal. If the JSON document cannot be marshaled, details are
// dropped so the output is still a valid error document.
func printError(w io.Writer, f output.Format, err error) {
	var cliError *clierr.Error
	if !errors.As(err, &cliError) {
		cliError = &clierr.Error{Code: clierr.Internal, Message: err.Error(), Cause: err}
	}

	if f == output.Table {
		_, _ = fmt.Fprintf(w, "%s\n", cliError.Message)
	} else {
		errorJSONDoc := output.NewErrorDoc(*cliError)
		jsonData, marshalErr := json.Marshal(errorJSONDoc)

		// If marshaling fails, remove details object as a fallback
		if marshalErr != nil {
			errorJSONDoc.Error.Details = nil
			jsonData, _ = json.Marshal(errorJSONDoc)
		}

		_, _ = fmt.Fprintf(w, "%s\n", jsonData)
	}
}
