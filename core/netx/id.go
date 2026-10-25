package netx

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func NewFlowID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return time.Now().UTC().Format("20060102150405.000000000")
}
