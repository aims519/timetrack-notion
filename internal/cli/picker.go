package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// loadAllTasks reads all tasks from the local cache, for populating a task picker
func loadAllTasks(ctx context.Context, s *store.Store, all bool, shortIDMap map[string]string, commandName string) ([]output.TaskItem, error) {
	allTasks, err := taskListForPicker(ctx, s, all, shortIDMap)
	if err != nil {
		return nil, err
	}

	if len(allTasks) == 0 {
		message := fmt.Sprintf("no tasks available to %s, try running 'tt task refresh'", commandName)
		if !all {
			message += fmt.Sprintf(". Or use --all in 'tt %s' to include completed tasks", commandName)
		}
		return nil, &clierr.Error{Code: clierr.NotFound, Message: message}
	}

	return allTasks, nil
}

// runPickerForm runs the task picker form and returns the task PageID selected
func runPickerForm(ctx context.Context, tasks []output.TaskItem, stdIn io.Reader, stdErr io.Writer) (string, error) {
	var selectedPageID string
	pickerForm := taskPickerForm(tasks, &selectedPageID).
		WithInput(stdIn).
		WithOutput(stdErr)

	err := pickerForm.RunWithContext(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, huh.ErrUserAborted) {
		return "", &clierr.Error{Code: clierr.Cancelled, Message: "task picker cancelled", Cause: err}
	}
	if err != nil {
		return "", &clierr.Error{Code: clierr.Internal, Message: "error running task picker form", Cause: err}
	}

	return selectedPageID, nil
}

// taskPickerForm returns a picker form that lists all available tasks the user can select from
func taskPickerForm(tasks []output.TaskItem, targetTaskID *string) *huh.Form {
	var taskOptions []huh.Option[string]

	longestOrder := 0
	longestShortID := 0
	for _, t := range tasks {
		if t.Order != nil {
			if orderLen := len(strconv.FormatFloat(*t.Order, 'f', -1, 64)); orderLen > longestOrder {
				longestOrder = orderLen
			}
		}

		if shortIDLen := len(t.ShortID); shortIDLen > longestShortID {
			longestShortID = shortIDLen
		}
	}

	for _, t := range tasks {
		var orderStr string
		if t.Order != nil {
			orderStr = strconv.FormatFloat(*t.Order, 'f', -1, 64)
		} else {
			orderStr = "-"
		}

		label := fmt.Sprintf("%-*s  %-*s  %s", longestOrder, orderStr, longestShortID, t.ShortID, t.Name)
		option := huh.NewOption(label, t.PageID)
		taskOptions = append(taskOptions, option)
	}

	keyMap := huh.NewDefaultKeyMap()
	keyMap.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"))

	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a task. Esc to cancel").
				Options(taskOptions...).
				Value(targetTaskID),
		),
	).WithKeyMap(keyMap)
}

// taskListForPicker retrieves the list of available tasks to be passed to the picker form
func taskListForPicker(ctx context.Context, s *store.Store, all bool, shortIDMap map[string]string) ([]output.TaskItem, error) {
	// Fetch all tasks
	tasks, _, err := s.ListTasks(ctx, store.NoLimit, all, nil)
	if err != nil {
		return nil, err
	}

	doc := mapDbTasks(tasks, shortIDMap, false, nil)

	return doc.Tasks, nil
}
