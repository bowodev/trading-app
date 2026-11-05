package memdb

type MemdbErr struct {
	msg string
}

func (e *MemdbErr) Error() string {
	return e.msg
}

func newMemdbErr(e string) *MemdbErr {
	return &MemdbErr{
		msg: e,
	}
}

var (
	ErrDataNotFound    = newMemdbErr("data not found")
	ErrInvalidData     = newMemdbErr("invalid data")
	ErrInvalidArgument = newMemdbErr("invalid argument")
)
