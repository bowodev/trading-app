package applications

import (
	"errors"

	"github.com/bowodev/trading-backend/apps/internal/port"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrStockNotFound   = errors.New("stock not found")
	ErrMatchingFailed  = errors.New("matching failed")
)

func mapInvalidArgument(err error) error {
	if errors.Is(err, port.ErrInvalidArgument) {
		return ErrInvalidArgument
	}

	return err
}
