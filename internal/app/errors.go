package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/tello-ai/tello-go/tello"
)

// Kind groups errors by how a caller should react. Each kind has one exit
// code (docs/design.md section 6); branch on Error.Code for detail.
type Kind string

const (
	KindInternal    Kind = "internal"
	KindUsage       Kind = "usage"
	KindAuth        Kind = "auth"
	KindRefused     Kind = "refused"
	KindInvalid     Kind = "invalid"
	KindCallEnded   Kind = "callEnded"
	KindConnection  Kind = "connection"
	KindServer      Kind = "server"
	KindHandler     Kind = "handler"
	KindTemporary   Kind = "temporary"
	KindInterrupted Kind = "interrupted"
)

var exitCodes = map[Kind]int{
	KindInternal:    1,
	KindUsage:       2,
	KindAuth:        3,
	KindRefused:     4,
	KindInvalid:     5,
	KindCallEnded:   6,
	KindConnection:  7,
	KindServer:      8,
	KindHandler:     9,
	KindTemporary:   75,
	KindInterrupted: 130,
}

// Error is the CLI's classified error. Commands return it (possibly wrapped);
// the root command reports it once and exits with ExitCode.
type Error struct {
	Kind     Kind
	Code     string
	Message  string
	Question string
	// Data is reported alongside the error, e.g. the callId of a call that
	// started before failing.
	Data any
}

func (e *Error) Error() string { return e.Message }

func (e *Error) ExitCode() int {
	if code, ok := exitCodes[e.Kind]; ok {
		return code
	}
	return 1
}

// Retryable reports whether the same request may succeed later.
func (e *Error) Retryable() bool { return e.Kind == KindTemporary }

func NewError(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Usagef reports invalid flags or arguments (exit 2).
func Usagef(format string, args ...any) *Error {
	return NewError(KindUsage, "usage", fmt.Sprintf(format, args...))
}

// ErrNoCredential is returned by App.APIKey when no API key is configured.
var ErrNoCredential = errors.New("no API key configured")

// Classify maps any error a command can return to an *Error. Gateway codes
// map per sdk-ws.v1 section 6: refusals that may succeed later become
// KindTemporary.
func Classify(err error) *Error {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.Canceled) {
		return NewError(KindInterrupted, "interrupted", "interrupted")
	}
	if errors.Is(err, ErrNoCredential) {
		return NewError(KindAuth, "apiKeyMissing", "no API key configured")
	}

	var (
		authErr       *tello.AuthenticationError
		validation    *tello.ValidationError
		rejected      *tello.CallRejectedError
		noActive      *tello.NoActiveCallError
		alreadyActive *tello.CallAlreadyActiveError
		refused       *tello.CallRefusedError
		provider      *tello.CallProviderError
		server        *tello.TelloServerError
		closed        *tello.ConnectionClosedError
		replaced      *tello.SessionReplacedError
	)
	switch {
	case errors.As(err, &authErr):
		return gatewayError(KindAuth, &authErr.TelloError, "unauthenticated")
	case errors.As(err, &validation):
		return gatewayError(KindInvalid, &validation.TelloError, "invalid")
	case errors.As(err, &rejected):
		return gatewayError(KindInvalid, &rejected.TelloError, "callRejected")
	case errors.As(err, &noActive):
		return gatewayError(KindInvalid, &noActive.TelloError, "noActiveCall")
	case errors.As(err, &alreadyActive):
		return gatewayError(KindTemporary, &alreadyActive.TelloError, "callAlreadyActive")
	case errors.As(err, &refused):
		if refused.Code == "concurrentLimitExceeded" {
			return gatewayError(KindTemporary, &refused.TelloError, "")
		}
		return gatewayError(KindRefused, &refused.TelloError, "")
	case errors.As(err, &provider):
		switch provider.Code {
		case "callProviderDraining", "callProviderUnavailable":
			return gatewayError(KindTemporary, &provider.TelloError, "")
		}
		return gatewayError(KindServer, &provider.TelloError, "")
	case errors.As(err, &server):
		return gatewayError(KindServer, &server.TelloError, "internalError")
	case errors.As(err, &closed):
		return &Error{Kind: KindConnection, Code: "connectionClosed", Message: messageOr(closed.Message, "connection closed")}
	case errors.As(err, &replaced):
		return &Error{Kind: KindConnection, Code: "sessionReplaced", Message: messageOr(replaced.Message, "session replaced by another connection")}
	}
	return NewError(KindInternal, "internal", err.Error())
}

func gatewayError(kind Kind, src *tello.TelloError, defaultCode string) *Error {
	code := src.Code
	if code == "" {
		code = defaultCode
	}
	return &Error{Kind: kind, Code: code, Message: messageOr(src.Message, code), Question: src.Question}
}

func messageOr(message, fallback string) string {
	if message == "" {
		return fallback
	}
	return message
}

// hints are human-mode follow-ups for codes the user can act on.
var hints = map[string]string{
	"apiKeyMissing":           "Run `tello auth login` or set TELLO_API_KEY.",
	"unauthenticated":         "Check the API key: `tello auth login` or TELLO_API_KEY.",
	"insufficientCredit":      "Top up call credit for this account. Retrying will not help.",
	"callerNotVerified":       "The callee number must be a verified number of this account.",
	"noRepresentativeNumber":  "Configure an outbound caller number for this account.",
	"concurrentLimitExceeded": "All concurrent call lines are busy. Retry after a call ends.",
	"callProviderDraining":    "The call provider is not accepting new calls. Retry later.",
	"callProviderUnavailable": "The call provider is unavailable. Retry later.",
	"callAlreadyActive":       "The previous call is still being cleaned up. Retry shortly.",
	"connectionFailed":        "Check the network and the gateway URL (--url / TELLO_URL).",
}
