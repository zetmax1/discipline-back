// Package apierr writes every API error body: {"error": <English text>, "code": <stable code>}.
// The web translates the code; the text is for logs and old clients. The codes are listed in
// discipline/contracts/errors.schema.json, and a test keeps both lists equal.
package apierr

import (
	"errors"

	"github.com/gin-gonic/gin"
)

// Code is a stable, snake_case error code. Never rename one: clients translate it.
type Code string

const (
	InternalError      Code = "internal_error"
	NotFound           Code = "not_found"
	MethodNotAllowed   Code = "method_not_allowed"
	BodyTooLarge       Code = "body_too_large"
	OriginNotAllowed   Code = "origin_not_allowed"
	RateLimited        Code = "rate_limited"
	ServiceUnavailable Code = "service_unavailable"
	ValidationFailed   Code = "validation_failed"

	AuthRequired             Code = "auth_required"
	TokenInvalid             Code = "token_invalid"
	Forbidden                Code = "forbidden"
	UserNotFound             Code = "user_not_found"
	InvalidCredentials       Code = "invalid_credentials"
	CaptchaRequired          Code = "captcha_required"
	LoginThrottled           Code = "login_throttled"
	EmailTaken               Code = "email_taken"
	PasswordTooShort         Code = "password_too_short"
	PasswordTooLong          Code = "password_too_long"
	CurrentPasswordRequired  Code = "current_password_required"
	CurrentPasswordIncorrect Code = "current_password_incorrect"
	InvalidTimezone          Code = "invalid_timezone"

	TaskNotFound            Code = "task_not_found"
	TaskIDInvalid           Code = "task_id_invalid"
	TaskNameLength          Code = "task_name_length"
	TaskDateInvalid         Code = "task_date_invalid"
	TaskTimeInvalid         Code = "task_time_invalid"
	TaskEndBeforeStart      Code = "task_end_before_start"
	TaskCategoryInvalid     Code = "task_category_invalid"
	TaskEnergyInvalid       Code = "task_energy_invalid"
	TaskStatusInvalid       Code = "task_status_invalid"
	TaskDistractionsInvalid Code = "task_distractions_invalid"
	TaskOverlap             Code = "task_overlap"
	PlanWeekInvalid         Code = "plan_week_invalid"
	TaskFocusRatingInvalid  Code = "task_focus_rating_invalid"
	TaskNoteTooLong         Code = "task_note_too_long"
	TaskNotMovable          Code = "task_not_movable"
	IdempotencyKeyRequired  Code = "idempotency_key_required"
	CheckinInvalid          Code = "checkin_invalid"
	SessionInvalid          Code = "session_invalid"
	SessionActive           Code = "session_active"
	ParkingNoteInvalid      Code = "parking_note_invalid"

	WordNotFound  Code = "word_not_found"
	WordIDInvalid Code = "word_id_invalid"
	DateInvalid   Code = "date_invalid"

	RangeInvalid              Code = "range_invalid"
	DaysInvalid               Code = "days_invalid"
	AnalyticsUnavailable      Code = "analytics_unavailable"
	AnalyticsValidationFailed Code = "analytics_validation_failed"
)

// All lists every code the API can send.
var All = []Code{
	InternalError, NotFound, MethodNotAllowed, BodyTooLarge, OriginNotAllowed, RateLimited, ServiceUnavailable,
	ValidationFailed, AuthRequired, TokenInvalid, Forbidden, UserNotFound, InvalidCredentials, CaptchaRequired,
	LoginThrottled, EmailTaken, PasswordTooShort, PasswordTooLong, CurrentPasswordRequired, CurrentPasswordIncorrect,
	InvalidTimezone, TaskNotFound, TaskIDInvalid, TaskNameLength, TaskDateInvalid, TaskTimeInvalid, TaskEndBeforeStart,
	TaskCategoryInvalid, TaskEnergyInvalid, TaskStatusInvalid, TaskDistractionsInvalid, TaskOverlap, PlanWeekInvalid,
	TaskFocusRatingInvalid, TaskNoteTooLong, TaskNotMovable, IdempotencyKeyRequired,
	CheckinInvalid, SessionInvalid, SessionActive, ParkingNoteInvalid,
	WordNotFound, WordIDInvalid, DateInvalid, RangeInvalid, DaysInvalid, AnalyticsUnavailable, AnalyticsValidationFailed,
}

func body(code Code, msg string, extra []gin.H) gin.H {
	h := gin.H{}
	for _, e := range extra {
		for k, v := range e {
			h[k] = v
		}
	}
	h["error"], h["code"] = msg, code
	return h
}

// JSON writes an error body with its code; extra fields (e.g. "details") are merged in.
func JSON(c *gin.Context, status int, code Code, msg string, extra ...gin.H) {
	c.JSON(status, body(code, msg, extra))
}

// Abort writes an error body with its code and stops the handler chain.
func Abort(c *gin.Context, status int, code Code, msg string, extra ...gin.H) {
	c.AbortWithStatusJSON(status, body(code, msg, extra))
}

// Error is a service error that carries a code for the handler to send.
type Error struct {
	Code Code
	base error
	msg  string
}

func (e *Error) Error() string {
	if e.base == nil {
		return e.msg
	}
	return e.base.Error() + ": " + e.msg
}
func (e *Error) Unwrap() error { return e.base }

// New returns a sentinel error with a code, for errors.Is checks in handlers.
func New(code Code, msg string) error {
	return &Error{Code: code, msg: msg}
}

// Wrap returns an error that matches base with errors.Is and carries code; its text is "<base>: <msg>".
func Wrap(base error, code Code, msg string) error {
	return &Error{Code: code, base: base, msg: msg}
}

// CodeOf returns the code carried by err or anything it wraps.
func CodeOf(err error) (Code, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}
