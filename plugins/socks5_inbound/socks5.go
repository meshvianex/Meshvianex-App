package socks5_inbound

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen      string `json:"listen"`
	AllowRemote bool   `json:"allow_remote,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.inbound.socks5", Name: "Local SOCKS5 Inbound", Version: "0.1.0", PluginTypes: []string{"inbound"}, RequiresCapabilities: []string{"socket.listen", "stream.read", "stream.write"}, Provides: []string{"inbound.socks5"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Local SOCKS5 Proxy", Fields: []plugin.ConfigField{{ID: "listen", Type: "host_port", Label: "Listen address", Default: "127.0.0.1:1080", Required: true}, {ID: "allow_remote", Type: "boolean", Label: "Allow remote clients", Default: false}}}}
}

func (Factory) Create(_ context.Context, ictx plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:1080"
	}
	if !cfg.AllowRemote && !isLoopbackListen(cfg.Listen) {
		return nil, fmt.Errorf("refusing non-loopback SOCKS listen without allow_remote=true")
	}
	service, ok := ictx.Services["realm.dialer"]
	if !ok {
		return nil, fmt.Errorf("realm.dialer service unavailable")
	}
	dialer, ok := service.(netx.Dialer)
	if !ok {
		return nil, fmt.Errorf("realm.dialer has wrong type")
	}
	return &Instance{cfg: cfg, dialer: dialer}, nil
}

type Instance struct {
	cfg    Config
	dialer netx.Dialer
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (i *Instance) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", i.cfg.Listen)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	i.ln, i.cancel = ln, cancel
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			i.wg.Add(1)
			go func() {
				defer i.wg.Done()
				defer conn.Close()
				_ = i.handle(ctx, conn)
			}()
		}
	}()
	return nil
}

func (i *Instance) Stop(context.Context) error {
	if i.cancel != nil {
		i.cancel()
	}
	if i.ln != nil {
		_ = i.ln.Close()
	}
	i.wg.Wait()
	return nil
}

func (i *Instance) handle(ctx context.Context, client net.Conn) error {
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil {
		return err
	}
	if greeting[0] != 0x05 {
		return fmt.Errorf("unsupported SOCKS version %d", greeting[0])
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(client, methods); err != nil {
		return err
	}
	noAuth := false
	for _, method := range methods {
		if method == 0x00 {
			noAuth = true
		}
	}
	if !noAuth {
		_, _ = client.Write([]byte{0x05, 0xff})
		return fmt.Errorf("SOCKS5 inbound supports no-authentication only")
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}
	var header [4]byte
	if _, err := io.ReadFull(client, header[:]); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 request version")
	}
	if header[1] != 0x01 {
		_ = writeReply(client, 0x07)
		return fmt.Errorf("SOCKS5 command is not CONNECT")
	}
	address, err := readAddress(client, header[3])
	if err != nil {
		_ = writeReply(client, 0x08)
		return err
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(client, portBytes[:]); err != nil {
		return err
	}
	address = net.JoinHostPort(address, fmt.Sprint(binary.BigEndian.Uint16(portBytes[:])))
	upstream, err := i.dialer.DialContext(ctx, netx.NewFlow("", "tcp", address, "user_traffic"))
	if err != nil {
		_ = writeReply(client, replyForError(err))
		return err
	}
	defer upstream.Close()
	if err := writeSuccessReply(client, upstream.LocalAddr()); err != nil {
		return err
	}
	_ = client.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	return nil
}

func readAddress(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		address := make([]byte, 4)
		_, err := io.ReadFull(r, address)
		return net.IP(address).String(), err
	case 0x04:
		address := make([]byte, 16)
		_, err := io.ReadFull(r, address)
		return net.IP(address).String(), err
	case 0x03:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return "", err
		}
		address := make([]byte, length[0])
		if _, err := io.ReadFull(r, address); err != nil {
			return "", err
		}
		return string(address), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS5 address type 0x%02x", atyp)
	}
}

func writeReply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func writeSuccessReply(w io.Writer, addr net.Addr) error {
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp != nil {
		ip := tcp.IP.To4()
		if ip != nil {
			return writeBytes(w, append([]byte{0x05, 0x00, 0x00, 0x01}, append(ip, byte(tcp.Port>>8), byte(tcp.Port))...))
		}
		ip = tcp.IP.To16()
		return writeBytes(w, append([]byte{0x05, 0x00, 0x00, 0x04}, append(ip, byte(tcp.Port>>8), byte(tcp.Port))...))
	}
	return writeReply(w, 0x00)
}

func writeBytes(w io.Writer, b []byte) error { _, err := w.Write(b); return err }

func replyForError(err error) byte {
	if strings.Contains(strings.ToLower(err.Error()), "refused") {
		return 0x05
	}
	if strings.Contains(strings.ToLower(err.Error()), "unreachable") {
		return 0x04
	}
	return 0x01
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
