package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type ErrorType string

const (
	ErrorTypeAuth            ErrorType = "auth"
	ErrorTypeRateLimit       ErrorType = "rate_limit"
	ErrorTypeNetwork         ErrorType = "network"
	ErrorTypeValidation      ErrorType = "validation"
	ErrorTypeProvider        ErrorType = "provider"
	ErrorTypeTimeout         ErrorType = "timeout"
	ErrorTypeQuota           ErrorType = "quota"
	ErrorTypeModel           ErrorType = "model"
	ErrorTypeInternal        ErrorType = "internal"
	ErrorTypeContextOverflow ErrorType = "context_overflow"
	ErrorTypeOverloaded      ErrorType = "overloaded"
)

type LiteLLMError struct {
	Type       ErrorType
	Code       string
	Message    string
	Provider   string
	Model      string
	StatusCode int
	Retryable  bool
	RetryAfter int
	Cause      error
}

func (e *LiteLLMError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if e.Code != "" && msg != "" {
		return e.Code + ": " + msg
	}
	if msg != "" && shouldShowCause(e) {
		if cause := strings.TrimSpace(e.Cause.Error()); cause != "" && cause != msg {
			return msg + ": " + cause
		}
	}
	if msg != "" {
		return msg
	}
	if e.Code != "" {
		return e.Code
	}
	if e.Type != "" {
		return string(e.Type)
	}
	return "litellm error"
}

func shouldShowCause(e *LiteLLMError) bool {
	if e == nil || e.Cause == nil {
		return false
	}
	return e.Type == ErrorTypeNetwork || e.Type == ErrorTypeTimeout
}

func (e *LiteLLMError) Unwrap() error {
	return e.Cause
}

func NewError(errorType ErrorType, message string) *LiteLLMError {
	return &LiteLLMError{Type: errorType, Message: message, Retryable: isRetryableByType(errorType)}
}

func NewErrorWithCause(errorType ErrorType, message string, cause error) *LiteLLMError {
	return &LiteLLMError{Type: errorType, Message: message, Cause: cause, Retryable: isRetryableByType(errorType)}
}

func NewProviderError(provider string, errorType ErrorType, message string) *LiteLLMError {
	return &LiteLLMError{Type: errorType, Provider: provider, Message: message, Retryable: isRetryableByType(errorType)}
}

func NewProviderErrorWithCause(provider string, errorType ErrorType, message string, cause error) *LiteLLMError {
	return &LiteLLMError{Type: errorType, Provider: provider, Message: message, Cause: cause, Retryable: isRetryableByType(errorType)}
}

func NewHTTPError(provider string, statusCode int, message string) *LiteLLMError {
	errorType := classifyHTTPError(statusCode)
	code, message := parseHTTPErrorMessage(message)
	return &LiteLLMError{
		Type:       errorType,
		Code:       code,
		Provider:   provider,
		Message:    message,
		StatusCode: statusCode,
		Retryable:  isRetryableByType(errorType),
	}
}

func parseHTTPErrorMessage(body string) (string, string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}

	var payload struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Error == nil {
		return "", body
	}

	switch e := payload.Error.(type) {
	case string:
		return "", strings.TrimSpace(e)
	case map[string]any:
		code := stringField(e, "code")
		msg := stringField(e, "message")
		if msg == "" {
			msg = body
		}
		return code, msg
	default:
		return "", body
	}
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func NewAuthError(provider, message string) *LiteLLMError {
	return NewProviderError(provider, ErrorTypeAuth, message)
}

func NewValidationError(provider, message string) *LiteLLMError {
	return NewProviderError(provider, ErrorTypeValidation, message)
}

func WrapValidationError(provider string, err error) error {
	if err == nil {
		return nil
	}
	var e *LiteLLMError
	if errors.As(err, &e) {
		if e.Provider == "" {
			copy := *e
			copy.Provider = provider
			return &copy
		}
		return err
	}
	return NewProviderErrorWithCause(provider, ErrorTypeValidation, err.Error(), err)
}

func NewRateLimitError(provider, message string, retryAfter int) *LiteLLMError {
	return &LiteLLMError{
		Type:       ErrorTypeRateLimit,
		Provider:   provider,
		Message:    message,
		Retryable:  true,
		RetryAfter: retryAfter,
	}
}

func NewModelError(provider, model, message string) *LiteLLMError {
	return &LiteLLMError{Type: ErrorTypeModel, Provider: provider, Model: model, Message: message}
}

func NewNetworkError(provider, message string, cause error) *LiteLLMError {
	if errors.Is(cause, context.Canceled) {
		return &LiteLLMError{Type: ErrorTypeNetwork, Provider: provider, Message: message, Cause: cause, Retryable: false}
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return &LiteLLMError{Type: ErrorTypeTimeout, Provider: provider, Message: message, Cause: cause, Retryable: false}
	}
	return &LiteLLMError{Type: ErrorTypeNetwork, Provider: provider, Message: message, Cause: cause, Retryable: true}
}

func NewTimeoutError(provider, message string) *LiteLLMError {
	return &LiteLLMError{Type: ErrorTypeTimeout, Provider: provider, Message: message, Retryable: true}
}

func IsAuthError(err error) bool            { return isErrorType(err, ErrorTypeAuth) }
func IsRateLimitError(err error) bool       { return isErrorType(err, ErrorTypeRateLimit) }
func IsNetworkError(err error) bool         { return isErrorType(err, ErrorTypeNetwork) }
func IsValidationError(err error) bool      { return isErrorType(err, ErrorTypeValidation) }
func IsProviderError(err error) bool        { return isErrorType(err, ErrorTypeProvider) }
func IsTimeoutError(err error) bool         { return isErrorType(err, ErrorTypeTimeout) }
func IsModelError(err error) bool           { return isErrorType(err, ErrorTypeModel) }
func IsContextOverflowError(err error) bool { return isErrorType(err, ErrorTypeContextOverflow) }
func IsOverloadedError(err error) bool      { return isErrorType(err, ErrorTypeOverloaded) }

func IsRetryableError(err error) bool {
	var e *LiteLLMError
	return errors.As(err, &e) && e.Retryable
}

func GetRetryAfter(err error) int {
	var e *LiteLLMError
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

func WrapError(err error, provider string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return NewNetworkError(provider, err.Error(), err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewNetworkError(provider, err.Error(), err)
	}
	var e *LiteLLMError
	if errors.As(err, &e) {
		if e.Provider == "" {
			copy := *e
			copy.Provider = provider
			return &copy
		}
		return err
	}
	return NewProviderErrorWithCause(provider, ErrorTypeProvider, err.Error(), err)
}

func isErrorType(err error, errorType ErrorType) bool {
	var e *LiteLLMError
	return errors.As(err, &e) && e.Type == errorType
}

func classifyHTTPError(statusCode int) ErrorType {
	switch {
	case statusCode == http.StatusUnauthorized, statusCode == http.StatusForbidden:
		return ErrorTypeAuth
	case statusCode == http.StatusTooManyRequests:
		return ErrorTypeRateLimit
	case statusCode == http.StatusPaymentRequired:
		return ErrorTypeQuota
	case statusCode == http.StatusNotFound:
		return ErrorTypeModel
	case statusCode == http.StatusRequestTimeout:
		return ErrorTypeTimeout
	case statusCode == http.StatusBadRequest:
		return ErrorTypeValidation
	case statusCode == 529:
		return ErrorTypeOverloaded
	case statusCode >= 500:
		return ErrorTypeProvider
	default:
		return ErrorTypeProvider
	}
}

func isRetryableByType(errorType ErrorType) bool {
	switch errorType {
	case ErrorTypeNetwork, ErrorTypeTimeout, ErrorTypeRateLimit, ErrorTypeOverloaded, ErrorTypeProvider:
		return true
	default:
		return false
	}
}
