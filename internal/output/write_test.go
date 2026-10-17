package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

type fakeDoc struct {
	Value   int    `json:"value"`
	Message string `json:"message"`
}

func (f fakeDoc) WriteTable(w io.Writer) error {
	_, err := fmt.Fprintln(w, "fake table output")
	return err
}

type unwritableDoc struct{}

var errUnwritableDoc error = errors.New("unable to write table output")

func (u unwritableDoc) WriteTable(w io.Writer) error {
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

	err := Write(w, JSON, doc)
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

	err := Write(w, Table, doc)
	if err != nil {
		t.Fatalf("error writing fakeDoc %v", err)
	}

	got := w.String()
	want := "fake table output\n"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrite_JSONFormatWithSpecialCharacters_WritesUnescapedOutput(t *testing.T) {
	doc := fakeDoc{Value: 5, Message: "R&D <x>"}
	w := &bytes.Buffer{}

	err := Write(w, JSON, doc)
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

	f := Format("xml")
	err := Write(w, f, doc)
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

	err := Write(w, Table, doc)
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

	err := Write(w, Table, doc)
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

	err := Write(w, JSON, doc)
	if err == nil {
		t.Fatal("writer failure not returned")
	}

	if !errors.Is(err, errWriteFailed) {
		t.Errorf("got %v, want %v", err, errWriteFailed)
	}
}
