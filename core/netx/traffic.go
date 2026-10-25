package netx

import (
	"context"
	"io"
	"net"
)

type Endpoint struct {
	Network string `json:"network"`
	Address string `json:"address"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
}
type Packet struct {
	Payload  []byte
	Metadata map[string]string
}

func (p Packet) Len() int { return len(p.Payload) }
func (p Packet) Clone() Packet {
	b := append([]byte(nil), p.Payload...)
	m := map[string]string{}
	for k, v := range p.Metadata {
		m[k] = v
	}
	return Packet{Payload: b, Metadata: m}
}

type Datagram struct {
	Payload  []byte
	Peer     net.Addr
	Metadata map[string]string
}
type Stream interface{ io.ReadWriteCloser }
type StreamOpener interface {
	OpenStream(context.Context, Flow) (Stream, error)
}

type TransformStage string

const (
	PreRoute      TransformStage = "pre_route"
	PostRoute     TransformStage = "post_route"
	PreProtocol   TransformStage = "pre_protocol"
	PostProtocol  TransformStage = "post_protocol"
	PreTransport  TransformStage = "pre_transport"
	PostTransport TransformStage = "post_transport"
	PreSocket     TransformStage = "pre_socket"
	StreamOpen    TransformStage = "stream_open"
	Handshake     TransformStage = "handshake"
	StreamData    TransformStage = "stream_data"
	StreamClose   TransformStage = "stream_close"
)

type TransformResult string

const (
	Continue TransformResult = "CONTINUE"
	Drop     TransformResult = "DROP"
	Replace  TransformResult = "REPLACE"
	Defer    TransformResult = "DEFER"
	Error    TransformResult = "ERROR"
)

type Transformer interface {
	Transform(context.Context, TransformStage, Packet, Flow) (Packet, TransformResult, error)
}
