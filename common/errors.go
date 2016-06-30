package common

import "fmt"

// UnrecoverableError for driver errors due to which the runtime will not be
// able to pick up any more jobs.
type UnrecoverableError interface {
	Unrecoverable() bool
}

func IsUnrecoverableError(err error) bool {
	ue, ok := err.(UnrecoverableError)
	return ok && ue.Unrecoverable()
}

// Errors that can be directly exposed to task creators/users.
type UserVisibleError interface {
	UserVisible() bool
	UserError() error
}

func IsUserVisibleError(err error) bool {
	ue, ok := err.(UserVisibleError)
	return ok && ue.UserVisible()
}

type userVisibleError struct {
	original error
	display  error
}

func (u *userVisibleError) Error() string       { return u.original.Error() }
func (u *userVisibleError) Unrecoverable() bool { return IsUnrecoverableError(u.original) }
func (u *userVisibleError) UserVisible() bool   { return true }
func (u *userVisibleError) UserError() error    { return u.display }

func NewUserVisibleError(err error, display error) error {
	return &userVisibleError{err, display}
}

// Make sure wrapperError implements forwards for any interfaces defined above.
type wrapperError struct {
	original error
	context  error
}

func (w *wrapperError) Unrecoverable() bool {
	return IsUnrecoverableError(w.original)
}

func (w *wrapperError) UserVisible() bool {
	return IsUserVisibleError(w.original)
}

func (u *wrapperError) UserError() error {
	if u.UserVisible() {
		return u.original.(UserVisibleError).UserError()
	}

	return nil
}

func (w *wrapperError) Error() string {
	return w.context.Error()
}

// Errorf that preserves the interfaces defined above.
func Errorf(format string, err error) error {
	// avoid accidents.
	if err == nil {
		return nil
	}

	return &wrapperError{
		original: err,
		context:  fmt.Errorf(format, err),
	}
}

func Cause(err error) error {
	if we, ok := err.(*wrapperError); ok {
		return Cause(we.original)
	}

	return err
}
