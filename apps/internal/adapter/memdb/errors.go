package memdb

import (
	"fmt"

	"github.com/bowodev/trading-backend/apps/internal/port"
)

var (
	ErrDataNotFound    = fmt.Errorf("%w: data not found", port.ErrNotFound)
	ErrInvalidData     = fmt.Errorf("%w: invalid data", port.ErrInvalidData)
	ErrInvalidArgument = fmt.Errorf("%w: invalid argument", port.ErrInvalidArgument)
)
