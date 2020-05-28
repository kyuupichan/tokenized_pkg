package txbuilder

import "fmt"

const (
	ErrorCodeInsufficientValue   = 1
	ErrorCodeWrongPrivateKey     = 2
	ErrorCodeMissingPrivateKey   = 3
	ErrorCodeWrongScriptTemplate = 4
	ErrorCodeBelowDustValue      = 5
	ErrorCodeDuplicateInput      = 6
	ErrorCodeMissingInputData    = 7
)

func IsErrorCode(err error, code int) bool {
	er, ok := err.(*txBuilderError)
	if !ok {
		return false
	}
	return er.code == code
}

func ErrorMessage(err error) string {
	er, ok := err.(*txBuilderError)
	if !ok {
		return ""
	}
	return er.message
}

type txBuilderError struct {
	code    int
	message string
}

func (err *txBuilderError) Error() string {
	if len(err.message) == 0 {
		return errorCodeString(err.code)
	}
	return fmt.Sprintf("%s : %s", errorCodeString(err.code), err.message)
}

func errorCodeString(code int) string {
	switch code {
	case ErrorCodeInsufficientValue:
		return "Insufficient Value"
	case ErrorCodeWrongPrivateKey:
		return "Wrong Private Key"
	case ErrorCodeMissingPrivateKey:
		return "Missing Private Key"
	case ErrorCodeWrongScriptTemplate:
		return "Wrong Script Template"
	case ErrorCodeBelowDustValue:
		return "Below Dust Value"
	case ErrorCodeDuplicateInput:
		return "Duplicate Input"
	default:
		return "Unknown Error Code"
	}
}

func newError(code int, message string) *txBuilderError {
	result := txBuilderError{code: code, message: message}
	return &result
}
