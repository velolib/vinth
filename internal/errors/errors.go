package errors

import (
	"fmt"
)

// AppError is a custom error type for centralized error handling
// It can wrap an underlying error and provide a code or category
// for more granular error management.
type AppError struct {
	Op   string // operation or context
	Code string // error code/category
	Err  error  // underlying error
}

func (e *AppError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Op, e.Err)
	}
	return fmt.Sprintf("[%s] %v", e.Code, e.Err)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// Helper to create a new AppError
func New(op, code string, err error) *AppError {
	return &AppError{Op: op, Code: code, Err: err}
}

// Helper to create a user-friendly message
func UserMessage(err error) string {
	if appErr, ok := err.(*AppError); ok {
		switch appErr.Code {
		case "network":
			return "Network error: please check your connection."
		case "notfound":
			return "Requested item not found."
		case "decode":
			return "Failed to parse server response."
		default:
			return appErr.Error()
		}
	}
	return err.Error()
}
