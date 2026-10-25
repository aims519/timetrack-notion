package cli

import (
	"errors"
	"io"
	"time"

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

// printError creates an ErrorDoc and passes it to output.Write,
// which handles writing for the specified format
func printError(w io.Writer, f output.Format, err error) {
	var cliError *clierr.Error
	if !errors.As(err, &cliError) {
		cliError = &clierr.Error{Code: clierr.Internal, Message: err.Error(), Cause: err}
	}

	errorDoc := output.NewErrorDoc(*cliError)

	// Error doc never shows local times, so pass in default UTC location
	writeErr := output.Write(w, f, errorDoc, time.UTC)

	// If writing fails, remove Details from errorDoc and try again
	if writeErr != nil {
		errorDoc.Error.Details = nil
		_ = output.Write(w, f, errorDoc, time.UTC)
	}
}
