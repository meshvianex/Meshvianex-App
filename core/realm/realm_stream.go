package realm

import (
	"context"
	"fmt"
	"net"
	"meshvianex/core/netx"
	"sync"
)

type transformedConn struct {
	net.Conn
	runtime       *Runtime
	ctx           context.Context
	flow          netx.Flow
	stateMu       sync.Mutex
	metadata      map[string]string
	pending       []byte
	handshakeOnce sync.Once
	handshakeErr  error
}

func (c *transformedConn) Read(p []byte) (int, error) {
	if err := c.ensureHandshake(); err != nil {
		return 0, err
	}
	c.stateMu.Lock()
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		c.stateMu.Unlock()
		return n, nil
	}
	c.stateMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	buf := make([]byte, len(p))
	n, err := c.Conn.Read(buf)
	if n == 0 {
		return n, err
	}
	packet := netx.Packet{Payload: buf[:n], Metadata: c.snapshotMetadata()}
	packet, result, transformErr := c.runtime.ApplyTransforms(c.ctx, netx.StreamData, packet, c.flow)
	if transformErr != nil {
		return 0, transformErr
	}
	if result == netx.Drop {
		return 0, fmt.Errorf("flow %s dropped by stream_data transform", c.flow.ID)
	}
	c.setMetadata(packet.Metadata)
	written := copy(p, packet.Payload)
	if written < len(packet.Payload) {
		c.stateMu.Lock()
		c.pending = append(c.pending, packet.Payload[written:]...)
		c.stateMu.Unlock()
	}
	if err != nil && written > 0 {
		return written, nil
	}
	return written, err
}

func (c *transformedConn) Write(p []byte) (int, error) {
	if err := c.ensureHandshake(); err != nil {
		return 0, err
	}
	packet := netx.Packet{Payload: append([]byte(nil), p...), Metadata: c.snapshotMetadata()}
	packet, result, err := c.runtime.ApplyTransforms(c.ctx, netx.StreamData, packet, c.flow)
	if err != nil {
		return 0, err
	}
	if result == netx.Drop {
		return 0, fmt.Errorf("flow %s dropped by stream_data transform", c.flow.ID)
	}
	c.setMetadata(packet.Metadata)
	return c.Conn.Write(packet.Payload)
}

func (c *transformedConn) Close() error {
	_, result, transformErr := c.runtime.ApplyTransforms(c.ctx, netx.StreamClose, netx.Packet{Metadata: c.snapshotMetadata()}, c.flow)
	closeErr := c.Conn.Close()
	if transformErr != nil {
		return transformErr
	}
	if result == netx.Error {
		return fmt.Errorf("flow %s stream_close transform failed", c.flow.ID)
	}
	return closeErr
}

// ensureHandshake runs the configured handshake transforms exactly once, after
// stream_open and immediately before the first application read or write.
// Keeping it lazy lets a caller establish a stream without forcing protocol
// bytes when it never sends application data.
func (c *transformedConn) ensureHandshake() error {
	c.handshakeOnce.Do(func() {
		packet, result, err := c.runtime.ApplyTransforms(c.ctx, netx.Handshake, netx.Packet{Metadata: c.snapshotMetadata()}, c.flow)
		if err != nil {
			c.handshakeErr = err
			return
		}
		if result == netx.Drop {
			c.handshakeErr = fmt.Errorf("flow %s dropped by handshake transform", c.flow.ID)
			return
		}
		c.setMetadata(packet.Metadata)
	})
	return c.handshakeErr
}

func (c *transformedConn) snapshotMetadata() map[string]string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return copyStringMap(c.metadata)
}

func (c *transformedConn) setMetadata(metadata map[string]string) {
	c.stateMu.Lock()
	c.metadata = copyStringMap(metadata)
	c.stateMu.Unlock()
}

func copyStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
