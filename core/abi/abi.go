package abi

import "fmt"

type Handle uint64
type Buffer struct {
	Bytes  []byte
	Length uint64
}

func NewBuffer(b []byte) Buffer { return Buffer{Bytes: b, Length: uint64(len(b))} }
func (b Buffer) Validate() error {
	if b.Length > uint64(len(b.Bytes)) {
		return fmt.Errorf("buffer length exceeds allocation")
	}
	return nil
}

type Code string

const (
	OK                    Code = "OK"
	InvalidArgument       Code = "ERR_INVALID_ARGUMENT"
	NotSupported          Code = "ERR_NOT_SUPPORTED"
	CapabilityDenied      Code = "ERR_CAPABILITY_DENIED"
	CapabilityUnavailable Code = "ERR_CAPABILITY_UNAVAILABLE"
	Timeout               Code = "ERR_TIMEOUT"
	Cancelled             Code = "ERR_CANCELLED"
	ResourceLimit         Code = "ERR_RESOURCE_LIMIT"
	Network               Code = "ERR_NETWORK"
	Protocol              Code = "ERR_PROTOCOL"
	InvalidState          Code = "ERR_INVALID_STATE"
	NotFound              Code = "ERR_NOT_FOUND"
	AlreadyExists         Code = "ERR_ALREADY_EXISTS"
	IO                    Code = "ERR_IO"
	Parse                 Code = "ERR_PARSE"
	Integrity             Code = "ERR_INTEGRITY"
	Trust                 Code = "ERR_TRUST"
	DependencyCycle       Code = "ERR_DEPENDENCY_CYCLE"
	Internal              Code = "ERR_INTERNAL"
)

type Error struct {
	Code    Code              `json:"code"`
	Message string            `json:"message,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

func (e Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

type Interfaces map[string]int

func (i Interfaces) Supports(name string, major int) bool { return i[name] >= major }
func Negotiate(required, supported Interfaces) error {
	for name, version := range required {
		if supported[name] < version {
			return Error{Code: NotSupported, Message: fmt.Sprintf("interface %s v%d is unavailable", name, version)}
		}
	}
	return nil
}
