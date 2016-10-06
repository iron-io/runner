package common

// Errors that can be directly exposed to task creators/users.
type UserVisibleError interface {
	UserVisible() bool
}

func IsUserVisibleError(err error) bool {
	ue, ok := err.(UserVisibleError)
	return ok && ue.UserVisible()
}

type userVisibleError struct {
	error
}

func (u *userVisibleError) UserVisible() bool { return true }

func UserError(err error) error {
	return &userVisibleError{err}
}
