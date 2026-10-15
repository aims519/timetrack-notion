package output

func ensureNotNil[T any](input []T) []T {
	if input == nil {
		return []T{}
	}
	return input
}
