package applications

import "github.com/oklog/ulid/v2"

const (
	defaultLimit = 10
	maxLimit     = 100
)

func parseCursor(raw string) (ulid.ULID, error) {
	if raw == "" {
		return ulid.ULID{}, nil
	}

	id, err := ulid.ParseStrict(raw)
	if err != nil {
		return ulid.ULID{}, ErrInvalidArgument
	}

	return id, nil
}

func encodeCursor(id ulid.ULID) string {
	if id == (ulid.ULID{}) {
		return ""
	}

	return id.String()
}

func normalizeLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > maxLimit {
		return maxLimit
	}

	return limit
}
