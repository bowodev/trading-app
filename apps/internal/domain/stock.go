package domain

import "github.com/oklog/ulid/v2"

type Stock struct {
	ID   ulid.ULID
	Code string
}
