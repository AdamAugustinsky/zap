package core

import (
	"errors"
	"strconv"
)

// The status codes of the core ABI (named as the C ABI zfcore.h named them,
// ZF_OK, ZF_EMPTY and the ZF_E_* errors, which the UI and its messages still
// use): what a refused or failed call's *Error carries.
const (
	StatusOK    int32 = 0
	StatusEmpty int32 = 1

	EArg       int32 = -1  // a null pointer, or an argument out of range
	ESmall     int32 = -2  // the event buffer is under ZF_MAX_RECORD (ZF_DRAIN_MIN)
	EMalformed int32 = -3  // a record that does not decode
	ETooLarge  int32 = -4  // a command over ZF_MAX_RECORD
	EVersion   int32 = -5  // the caller's ZF_ABI or schema hash differs
	EPoisoned  int32 = -6  // an earlier call on this core panicked
	EPanic     int32 = -7  // this call panicked; the core is now poisoned
	ENotFound  int32 = -8  // no such blob, or no frame
	ERange     int32 = -9  // a blob offset past its end
	EState     int32 = -10 // started twice, or a command this core does not answer
	EIO        int32 = -11 // the core could not create its pipe or threads
	ETimeout   int32 = -12 // zf_core_close's timeout passed; closing goes on
	ERefused   int32 = -13 // the command's fields are inconsistent
)

var codeNames = [...]string{"ZF_E_ARG", "ZF_E_SMALL", "ZF_E_MALFORMED", "ZF_E_TOO_LARGE", "ZF_E_VERSION",
	"ZF_E_POISONED", "ZF_E_PANIC", "ZF_E_NOT_FOUND", "ZF_E_RANGE", "ZF_E_STATE", "ZF_E_IO", "ZF_E_TIMEOUT", "ZF_E_REFUSED"}

// CodeName returns the ABI name of a status code.
func CodeName(code int32) string {
	switch {
	case code == StatusOK:
		return "ZF_OK"
	case code == StatusEmpty:
		return "ZF_EMPTY"
	case code < 0 && int(-code) <= len(codeNames):
		return codeNames[-code-1]
	}
	return "status " + strconv.Itoa(int(code))
}

var (
	// ErrClosed is returned by every call on a core after Close.
	ErrClosed = errors.New("zapfast core: closed")
)

// Error is a call the core refused or failed, with its code and message.
type Error struct {
	Op   string // the call, named as the C ABI named it (zf_send, ...)
	Code int32  // a ZF_E_* code
	Msg  string
}

func (e *Error) Error() string {
	return "zapfast core: " + e.Op + ": " + e.Msg + " (" + CodeName(e.Code) + ")"
}

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code int32) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
