package port

import "errors"

var (
	ErrNotFound        = errors.New("not found")
	ErrInvalidData     = errors.New("invalid data")
	ErrInvalidArgument = errors.New("invalid argument")
)
