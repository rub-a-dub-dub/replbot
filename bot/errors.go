package bot

import "fmt"

// BotError represents a general bot error with a code for programmatic handling.
type BotError struct {
	Code    string
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *BotError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap returns the underlying cause.
func (e *BotError) Unwrap() error {
	return e.Cause
}

// SessionError represents session specific errors.
type SessionError struct{ *BotError }

// ConfigError represents configuration errors.
type ConfigError struct{ *BotError }

// ValidationError represents input validation errors.
type ValidationError struct{ *BotError }

// Constructors for convenience.
func NewBotError(code, message string, cause error) *BotError {
	return &BotError{Code: code, Message: message, Cause: cause}
}

func NewSessionError(code, message string, cause error) *SessionError {
	return &SessionError{NewBotError(code, message, cause)}
}

func NewConfigError(code, message string, cause error) *ConfigError {
	return &ConfigError{NewBotError(code, message, cause)}
}

func NewValidationError(code, message string, cause error) *ValidationError {
	return &ValidationError{NewBotError(code, message, cause)}
}
