package mustache

import (
	"fmt"
)

// ErrorCode is the list of allowed values for the error's code.
type ErrorCode string

// List of values that ErrorCode can take.
const (
	ErrUnmatchedOpenTag      ErrorCode = "unmatched_open_tag"
	ErrEmptyTag              ErrorCode = "empty_tag"
	ErrSectionNoClosingTag   ErrorCode = "section_no_closing_tag"
	ErrInterleavedClosingTag ErrorCode = "interleaved_closing_tag"
	ErrInvalidMetaTag        ErrorCode = "invalid_meta_tag"
	ErrUnmatchedCloseTag     ErrorCode = "unmatched_close_tag"
	ErrInvalidVariable       ErrorCode = "invalid_variable"
)

// ParseError represents an error during the parsing
type ParseError struct {
	// Line contains the line of the error
	Line int
	// Code contains the error code of the error
	Code ErrorCode
	// Reason contains the name of the element generating the error
	Reason string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.defaultMessage())
}

func (e ParseError) defaultMessage() string {
	switch e.Code {
	case ErrUnmatchedOpenTag:
		return "unmatched open tag"
	case ErrEmptyTag:
		return "empty tag"
	case ErrSectionNoClosingTag:
		return fmt.Sprintf("Section %s has no closing tag", e.Reason)
	case ErrInterleavedClosingTag:
		return fmt.Sprintf("interleaved closing tag: %s", e.Reason)
	case ErrInvalidMetaTag:
		return "Invalid meta tag"
	case ErrUnmatchedCloseTag:
		return "unmatched close tag"
	case ErrInvalidVariable:
		return "invalid variable"
	default:
		return "unknown error"
	}
}

func newError(line int, code ErrorCode) ParseError {
	return ParseError{
		Line: line,
		Code: code,
	}
}

func newErrorWithReason(line int, code ErrorCode, reason string) ParseError {
	return ParseError{
		Line:   line,
		Code:   code,
		Reason: reason,
	}
}

type MissingVariableError struct {
	Name string
}

func IsMissingVariableError(err error) bool {
	_, ok := err.(MissingVariableError)
	return ok
}

func (e MissingVariableError) Error() string {
	return fmt.Sprintf("missing variable %q", e.Name)
}

func newMissingVariableError(name string) MissingVariableError {
	return MissingVariableError{
		Name: name,
	}
}

type InvalidVariableError struct {
	Name string
}

func IsInvalidVariableError(err error) bool {
	_, ok := err.(InvalidVariableError)
	return ok
}

func (e InvalidVariableError) Error() string {
	return fmt.Sprintf("invalid variable %q", e.Name)
}

func newInvalidVariableError(name string) InvalidVariableError {
	return InvalidVariableError{
		Name: name,
	}
}
