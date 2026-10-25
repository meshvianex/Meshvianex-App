package xray_outbound

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/plugins/socks5_outbound"

	xraycore "github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

const (
	pluginID             = "io.meshvianex.outbound.xray"
	defaultStartupSecs   = 15
	defaultShutdownSecs  = 5
	maximumLifecycleSecs = 300
	startupAttempts      = 4
	readinessPoll        = 20 * time.Millisecond
	xrayAssetEnv         = "XRAY_LOCATION_ASSET"
	xrayWorkerEnv        = "MESHVIANEX_XRAY_WORKER"
	maxWorkerConfigBytes = 64 << 20
)

const WorkerModeEnv = xrayWorkerEnv

type Config struct {
	ConfigPath             string          "json:\"config_path\""
	InlineConfig           json.RawMessage "json:\"inline_config,omitempty\""
	AssetDirectory         string          "json:\"asset_directory,omitempty\""
	StartupTimeoutSeconds  int             "json:\"startup_timeout_seconds,omitempty\""
	ShutdownTimeoutSeconds int             "json:\"shutdown_timeout_seconds,omitempty\""
}

type Factory struct{}

const defaultInlineConfig = "{\"outbounds\":[{\"protocol\":\"freedom\",\"tag\":\"meshvianex-egress\"}]}"

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:                   pluginID,
		Name:                 "Xray Outbound",
		Version:              "0.2.0",
		PluginTypes:          []string{"outbound"},
		RequiresCapabilities: []string{"socket.tcp", "socket.listen", "config.read", "process.spawn"},
		Provides:             []string{"outbound.xray.tcp"},
		ABI:                  1,
		ConfigSchema: &plugin.ConfigSchema{
			Title:       "Xray TCP Outbound",
			Description: "Runs Xray-core inside Meshvianex and accepts TCP connections through a private loopback SOCKS bridge.",
			Fields: []plugin.ConfigField{
				{ID: "config_path", Type: "path", Label: "Xray configuration", Description: "Optional. Leave blank to use the inline configuration below."},
				{ID: "inline_config", Type: "object", Label: "Inline Xray configuration", Default: map[string]any{"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "meshvianex-egress"}}}, Fields: []plugin.ConfigField{{ID: "outbounds", Type: "array", Label: "Outbounds", ItemFields: []plugin.ConfigField{{ID: "protocol", Type: "text", Label: "Protocol", Required: true}, {ID: "tag", Type: "text", Label: "Tag", Required: true}}}}},
				{ID: "asset_directory", Type: "path", Label: "Xray asset directory", Description: "Optional directory for Xray data files."},
				{ID: "startup_timeout_seconds", Type: "number", Label: "Startup timeout (seconds)", Default: defaultStartupSecs},
				{ID: "shutdown_timeout_seconds", Type: "number", Label: "Shutdown timeout (seconds)", Default: defaultShutdownSecs},
			},
		},
	}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("xray config: %w", err)
	}
	if cfg.ConfigPath != "" {
		configBytes, err := os.ReadFile(cfg.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("read xray config: %w", err)
		}
		cfg.InlineConfig = configBytes
	}
	if len(cfg.InlineConfig) == 0 {
		cfg.InlineConfig = json.RawMessage(defaultInlineConfig)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(cfg.InlineConfig, &source); err != nil {
		return nil, fmt.Errorf("parse inline xray config: %w", err)
	}
	if source == nil {
		return nil, fmt.Errorf("xray config must be a JSON object")
	}
	if inbounds, ok := source["inbounds"]; ok {
		var configured []json.RawMessage
		if err := json.Unmarshal(inbounds, &configured); err != nil {
			return nil, fmt.Errorf("xray inbounds must be an array: %w", err)
		}
		if len(configured) != 0 {
			return nil, fmt.Errorf("xray inbounds are managed by Meshvianex; remove user-provided inbounds")
		}
	}
	outbounds, ok := source["outbounds"]
	if !ok {
		return nil, fmt.Errorf("xray outbounds must be a non-empty array")
	}
	var configuredOutbounds []json.RawMessage
	if err := json.Unmarshal(outbounds, &configuredOutbounds); err != nil || len(configuredOutbounds) == 0 {
		return nil, fmt.Errorf("xray outbounds must be a non-empty array")
	}
	if cfg.StartupTimeoutSeconds <= 0 {
		cfg.StartupTimeoutSeconds = defaultStartupSecs
	}
	if cfg.ShutdownTimeoutSeconds <= 0 {
		cfg.ShutdownTimeoutSeconds = defaultShutdownSecs
	}
	if cfg.StartupTimeoutSeconds > maximumLifecycleSecs || cfg.ShutdownTimeoutSeconds > maximumLifecycleSecs {
		return nil, fmt.Errorf("xray lifecycle timeouts must be at most %d seconds", maximumLifecycleSecs)
	}
	return &Instance{cfg: cfg, source: source, state: stateNew}, nil
}

type instanceState uint8

const (
	stateNew instanceState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
	stateFailed
)

type startResult struct {
	worker *workerProcess
	proxy  netx.Dialer
	port   int
}

type workerProcess struct {
	cmd   *exec.Cmd
	input *io.PipeWriter
	done  chan error
}

type Instance struct {
	cfg    Config
	source map[string]json.RawMessage

	mu        sync.Mutex
	state     instanceState
	worker    *workerProcess
	proxy     netx.Dialer
	port      int
	starting  context.CancelFunc
	startDone chan struct{}
	stopDone  chan struct{}
	stopErr   error
}

var (
	_ netx.Dialer     = (*Instance)(nil)
	_ plugin.Instance = (*Instance)(nil)
)

func (i *Instance) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	switch i.state {
	case stateRunning:
		i.mu.Unlock()
		return nil
	case stateStarting:
		i.mu.Unlock()
		return fmt.Errorf("xray instance is already starting")
	case stateStopping:
		i.mu.Unlock()
		return fmt.Errorf("xray instance is stopping")
	}
	startCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	i.state = stateStarting
	i.starting = cancel
	i.startDone = done
	i.mu.Unlock()

	result, err := i.startRuntime(startCtx)
	startErr := startCtx.Err()
	cancel()
	if err == nil && startErr != nil {
		_ = result.worker.stop(context.Background())
		err = startErr
	}

	i.mu.Lock()
	if err != nil {
		i.state = stateFailed
	} else {
		i.worker = result.worker
		i.proxy = result.proxy
		i.port = result.port
		i.state = stateRunning
	}
	i.starting = nil
	i.startDone = nil
	close(done)
	i.mu.Unlock()
	return err
}

func (i *Instance) startRuntime(ctx context.Context) (startResult, error) {
	startCtx, cancel := context.WithTimeout(ctx, time.Duration(i.cfg.StartupTimeoutSeconds)*time.Second)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < startupAttempts; attempt++ {
		if err := startCtx.Err(); err != nil {
			return startResult{}, fmt.Errorf("xray startup: %w", err)
		}
		port, err := reserveLoopbackPort()
		if err != nil {
			lastErr = fmt.Errorf("reserve loopback SOCKS port: %w", err)
			continue
		}
		configBytes, err := i.runtimeConfig(port)
		if err != nil {
			return startResult{}, err
		}
		proxy, err := i.newProxyDialer(port)
		if err != nil {
			return startResult{}, err
		}
		worker, err := startWorker(startCtx, configBytes, i.cfg.AssetDirectory)
		if err != nil {
			lastErr = err
			continue
		}
		endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if err := waitReady(startCtx, endpoint); err != nil {
			_ = worker.stop(context.Background())
			lastErr = err
			if startCtx.Err() != nil {
				return startResult{}, fmt.Errorf("xray loopback readiness: %w (probe: %v)", startCtx.Err(), err)
			}
			continue
		}
		if err := startCtx.Err(); err != nil {
			_ = worker.stop(context.Background())
			return startResult{}, fmt.Errorf("xray startup: %w", err)
		}
		return startResult{worker: worker, proxy: proxy, port: port}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("embedded Xray did not become ready")
	}
	return startResult{}, fmt.Errorf("xray startup failed after %d attempts: %w", startupAttempts, lastErr)
}

func (i *Instance) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	stopCtx, cancel := context.WithTimeout(ctx, time.Duration(i.cfg.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()

	i.mu.Lock()
	if i.state == stateStarting {
		cancelStart, done := i.starting, i.startDone
		i.mu.Unlock()
		if cancelStart != nil {
			cancelStart()
		}
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return fmt.Errorf("stop xray while starting: %w", stopCtx.Err())
		}
	}
	if i.state == stateStopping {
		done := i.stopDone
		i.mu.Unlock()
		return i.waitStopped(stopCtx, done)
	}
	if i.state != stateRunning || i.worker == nil {
		i.mu.Unlock()
		return nil
	}
	i.state = stateStopping
	done := make(chan struct{})
	i.stopDone = done
	worker := i.worker
	i.mu.Unlock()

	go i.closeRuntime(stopCtx, worker, done)
	return i.waitStopped(stopCtx, done)
}

func (i *Instance) closeRuntime(ctx context.Context, worker *workerProcess, done chan struct{}) {
	err := worker.stop(ctx)
	i.mu.Lock()
	if i.worker == worker {
		i.worker = nil
		i.proxy = nil
		i.port = 0
		i.state = stateStopped
	}
	i.stopErr = err
	close(done)
	i.mu.Unlock()
}

func (w *workerProcess) stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	_ = w.input.Close()
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		<-w.done
		return fmt.Errorf("stop xray worker: %w", ctx.Err())
	}
}

func startWorker(ctx context.Context, config []byte, assetDirectory string) (*workerProcess, error) {
	if len(config) == 0 || len(config) > maxWorkerConfigBytes {
		return nil, fmt.Errorf("xray worker configuration size must be between 1 byte and %d bytes", maxWorkerConfigBytes)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Meshvianex executable for Xray worker: %w", err)
	}
	reader, input := io.Pipe()
	args := []string{}
	program := strings.ToLower(os.Args[0])
	if strings.HasSuffix(program, ".test") || strings.HasSuffix(program, ".test.exe") {
		args = append(args, "-test.run=^TestXrayWorkerProcess$")
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = workerEnvironment(assetDirectory)
	cmd.Stdin = reader
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("open xray worker output: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("start xray worker: %w", err)
	}
	go func() {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(config)))
		_, _ = input.Write(size[:])
		_, _ = input.Write(config)
	}()
	response := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		response <- line
	}()
	select {
	case line := <-response:
		if line == "READY\n" {
			worker := &workerProcess{cmd: cmd, input: input, done: make(chan error, 1)}
			go func() { worker.done <- cmd.Wait() }()
			return worker, nil
		}
		_ = input.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("xray worker startup failed: %s", line)
	case <-ctx.Done():
		_ = input.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return nil, fmt.Errorf("xray worker startup: %w", ctx.Err())
	}
}

func workerEnvironment(assetDirectory string) []string {
	allowed := []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "HOME"}
	env := make([]string, 0, len(allowed)+2)
	for _, name := range allowed {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, xrayWorkerEnv+"=1")
	if assetDirectory != "" {
		env = append(env, xrayAssetEnv+"="+assetDirectory)
	}
	return env
}

// RunWorker starts one Xray server in its own Meshvianex subprocess. The parent
// owns the process lifetime by keeping the input pipe open until Stop.
func RunWorker(input io.Reader, output io.Writer) error {
	var size [4]byte
	if _, err := io.ReadFull(input, size[:]); err != nil {
		return fmt.Errorf("read xray worker config size: %w", err)
	}
	configSize := binary.BigEndian.Uint32(size[:])
	if configSize == 0 || configSize > maxWorkerConfigBytes {
		return fmt.Errorf("invalid xray worker configuration size %d", configSize)
	}
	configBytes := make([]byte, configSize)
	if _, err := io.ReadFull(input, configBytes); err != nil {
		return fmt.Errorf("read xray worker config: %w", err)
	}
	config, err := xraycore.LoadConfig("json", bytes.NewReader(configBytes))
	if err != nil {
		return fmt.Errorf("parse Xray configuration: %w", err)
	}
	server, err := xraycore.NewWithContext(context.Background(), config)
	if err != nil {
		return err
	}
	if err := server.Start(); err != nil {
		_ = server.Close()
		return fmt.Errorf("start embedded Xray: %w", err)
	}
	if _, err := io.WriteString(output, "READY\n"); err != nil {
		_ = server.Close()
		return fmt.Errorf("notify parent that Xray started: %w", err)
	}
	_, readErr := io.Copy(io.Discard, input)
	closeErr := server.Close()
	if readErr != nil {
		return fmt.Errorf("wait for xray worker shutdown: %w", readErr)
	}
	return closeErr
}

func (i *Instance) waitStopped(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		i.mu.Lock()
		err := i.stopErr
		i.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop xray: %w", ctx.Err())
	}
}

func (i *Instance) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	if i.state != stateRunning || i.proxy == nil {
		state := i.state
		i.mu.Unlock()
		return nil, fmt.Errorf("xray outbound is not running (state %d)", state)
	}
	proxy := i.proxy
	i.mu.Unlock()
	conn, err := proxy.DialContext(ctx, flow)
	if err != nil {
		return nil, fmt.Errorf("xray SOCKS bridge: %w", err)
	}
	return conn, nil
}

func (i *Instance) runtimeConfig(port int) ([]byte, error) {
	root := make(map[string]json.RawMessage, len(i.source)+1)
	for key, value := range i.source {
		root[key] = value
	}
	inbound := []map[string]any{{
		"tag":      "meshvianex-socks",
		"listen":   "127.0.0.1",
		"port":     port,
		"protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": false},
	}}
	encodedInbound, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("encode Meshvianex Xray inbound: %w", err)
	}
	root["inbounds"] = encodedInbound
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode Meshvianex Xray config: %w", err)
	}
	return encoded, nil
}

func (i *Instance) newProxyDialer(port int) (netx.Dialer, error) {
	raw, err := json.Marshal(socks5_outbound.Config{
		URL:            "socks5://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		TimeoutSeconds: i.cfg.StartupTimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("create xray SOCKS bridge config: %w", err)
	}
	inst, err := (socks5_outbound.Factory{}).Create(context.Background(), plugin.InstanceContext{}, raw)
	if err != nil {
		return nil, fmt.Errorf("create xray SOCKS bridge: %w", err)
	}
	dialer, ok := inst.(netx.Dialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS bridge does not implement netx.Dialer")
	}
	return dialer, nil
}

func waitReady(ctx context.Context, endpoint string) error {
	ticker := time.NewTicker(readinessPoll)
	defer ticker.Stop()
	var lastErr error
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", endpoint)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w (last loopback dial: %v)", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}
