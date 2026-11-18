package settings

import "errors"

// ErrNotFound is returned when the singleton row does not exist.
var ErrNotFound = errors.New("settings: not found")
