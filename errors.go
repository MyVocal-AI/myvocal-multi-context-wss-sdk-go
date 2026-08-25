package myvocalmultiwss

import "fmt"

type ValidationError struct{ Message string }

func (err *ValidationError) Error() string { return err.Message }

type StateError struct{ Message string }

func (err *StateError) Error() string { return err.Message }

type TransportError struct {
	Message string
	Cause   error
}

func (err *TransportError) Error() string {
	if err.Cause == nil {
		return err.Message
	}
	return fmt.Sprintf("%s: %v", err.Message, err.Cause)
}

func (err *TransportError) Unwrap() error { return err.Cause }

type AuthenticationError struct{ Message string }

func (err *AuthenticationError) Error() string { return err.Message }

type TimeoutError struct {
	Message string
	Cause   error
}

func (err *TimeoutError) Error() string {
	if err.Cause == nil {
		return err.Message
	}
	return fmt.Sprintf("%s: %v", err.Message, err.Cause)
}

func (err *TimeoutError) Unwrap() error { return err.Cause }

type ProtocolError struct {
	Code      string
	Field     string
	Message   string
	ContextID string
	CloseCode int
}

func (err *ProtocolError) Error() string {
	if err.Message != "" {
		return err.Message
	}
	return err.Code
}

type CallbackError struct {
	Callback string
	Cause    any
}

func (err *CallbackError) Error() string {
	return fmt.Sprintf("%s callback failed: %v", err.Callback, err.Cause)
}
