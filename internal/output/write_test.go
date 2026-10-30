package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

type fakeDoc struct {
	Value   int    `json:"value"`
	Message string `json:"message"`
}

func (f fakeDoc) WriteTable(w io.Writer, _ *time.Location) error {
	_, err := fmt.Fprintln(w, "fake table output")
	return err
}

// warnedDoc embeds BaseWarnings, so it lists its warnings like a real document
type warnedDoc struct {
	BaseWarnings
}

func (d warnedDoc) WriteTable(w io.Writer, _ *time.Location) error {
	_, err := fmt.Fprintln(w, "warned table output")
	return err
}

type unwritableDoc struct{}

var errUnwritableDoc error = errors.New("unable to write table output")

func (u unwritableDoc) WriteTable(w io.Writer, _ *time.Location) error {
	return errUnwritableDoc
}

type failingWriter struct{}

var errWriteFailed error = errors.New("write failed")

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errWriteFailed
}

func TestWrite_JSONFormat_WritesExpectedOutput(t *testing.T) {
	doc := fakeDoc{Value: 21, Message: "twenty one"}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, JSON, doc, time.UTC)
	if err != nil {
		t.Fatalf("error writing fakeDoc %v", err)
	}

	got := w.String()
	want := "{\"value\":21,\"message\":\"twenty one\"}\n"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrite_TableFormat_WritesExpectedOutput(t *testing.T) {
	doc := fakeDoc{}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, Table, doc, time.UTC)
	if err != nil {
		t.Fatalf("error writing fakeDoc %v", err)
	}

	got := w.String()
	want := "fake table output\n"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// fakeDoc has no warnings to list
	if stderr.Len() != 0 {
		t.Errorf("stderr want empty, got %q", stderr.String())
	}
}

func TestWrite_JSONFormatWithSpecialCharacters_WritesUnescapedOutput(t *testing.T) {
	doc := fakeDoc{Value: 5, Message: "R&D <x>"}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, JSON, doc, time.UTC)
	if err != nil {
		t.Fatalf("error writing fakeDoc %v", err)
	}

	got := w.String()
	want := `{"value":5,"message":"R&D <x>"}` + "\n"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrite_UnknownFormat_ReturnsError(t *testing.T) {
	doc := fakeDoc{Value: 21, Message: "twenty one"}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	f := Format("xml")
	err := Write(w, stderr, f, doc, time.UTC)
	if err == nil {
		t.Fatalf("unknown format did not throw an error %q", f)
	}

	if w.Len() != 0 {
		t.Errorf("output written for unknown format")
	}
}

func TestWrite_ErrorDuringDocTableWrite_ReturnsError(t *testing.T) {
	doc := unwritableDoc{}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, Table, doc, time.UTC)
	if err == nil {
		t.Fatal("error writing unwritable doc not returned")
	}

	if !errors.Is(err, errUnwritableDoc) {
		t.Errorf("got %q, want %q", err, errUnwritableDoc)
	}
}

func TestWrite_WriterErrorTableFormat_ReturnsError(t *testing.T) {
	doc := fakeDoc{}
	w := failingWriter{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, Table, doc, time.UTC)
	if err == nil {
		t.Fatal("writer failure not returned")
	}

	if !errors.Is(err, errWriteFailed) {
		t.Errorf("got %v, want %v", err, errWriteFailed)
	}
}

func TestWrite_WriterErrorJSONFormat_ReturnsError(t *testing.T) {
	doc := fakeDoc{}
	w := failingWriter{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, JSON, doc, time.UTC)
	if err == nil {
		t.Fatal("writer failure not returned")
	}

	if !errors.Is(err, errWriteFailed) {
		t.Errorf("got %v, want %v", err, errWriteFailed)
	}
}

func TestWrite_TableFormatWithWarnings_WritesWarningsToStderr(t *testing.T) {
	tests := []struct {
		name       string
		warnings   []Warning
		wantStderr string
	}{
		{"nil", nil, ""},
		{"empty", []Warning{}, ""},
		{"one_warning", []Warning{{Code: StaleTimer, Message: "timer has been running for over 12 hours"}}, "warning: timer has been running for over 12 hours\n"},
		{"two_warnings", []Warning{
			{Code: StaleTimer, Message: "first"},
			{Code: Overlap, Message: "second"},
		}, "warning: first\nwarning: second\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := warnedDoc{BaseWarnings{Warnings: tc.warnings}}
			w := &bytes.Buffer{}
			stderr := &bytes.Buffer{}

			err := Write(w, stderr, Table, doc, time.UTC)
			if err != nil {
				t.Fatalf("error writing warnedDoc %v", err)
			}

			// Warnings go to stderr only, so the table on stdout is unchanged (DESIGN.md §8)
			if got, want := w.String(), "warned table output\n"; got != want {
				t.Errorf("stdout got %q, want %q", got, want)
			}
			if got := stderr.String(); got != tc.wantStderr {
				t.Errorf("stderr got %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestWrite_JSONFormatWithWarnings_NothingOnStderr(t *testing.T) {
	doc := warnedDoc{BaseWarnings{Warnings: []Warning{{Code: StaleTimer, Message: "timer has been running for over 12 hours"}}}}
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, JSON, doc, time.UTC)
	if err != nil {
		t.Fatalf("error writing warnedDoc %v", err)
	}

	// In JSON the warnings are part of the document
	if !strings.Contains(w.String(), `"code":"stale_timer"`) {
		t.Errorf("stdout want the warning in the document, got %q", w.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr want empty, got %q", stderr.String())
	}
}

func TestWrite_ErrorDocTableFormat_NothingOnStderr(t *testing.T) {
	// printError passes stderr as both writers, so use separate buffers to tell them apart
	doc := NewErrorDoc(clierr.Error{Code: clierr.NoTimerRunning, Message: "no timer running"})
	w := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	err := Write(w, stderr, Table, doc, time.UTC)
	if err != nil {
		t.Fatalf("error writing ErrorDoc %v", err)
	}

	if got, want := w.String(), "no timer running\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr want empty, got %q", stderr.String())
	}
}

func TestWrite_StderrWriterError_ReturnsError(t *testing.T) {
	doc := warnedDoc{BaseWarnings{Warnings: []Warning{{Code: StaleTimer, Message: "timer has been running for over 12 hours"}}}}
	w := &bytes.Buffer{}
	stderr := failingWriter{}

	err := Write(w, stderr, Table, doc, time.UTC)
	if err == nil {
		t.Fatal("stderr writer failure not returned")
	}

	if !errors.Is(err, errWriteFailed) {
		t.Errorf("got %v, want %v", err, errWriteFailed)
	}
}
