package backup

import "errors"

// ErrNotFound is returned when a backup config row does not exist.
var ErrNotFound = errors.New("backup: not found")
