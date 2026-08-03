package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

var versionRE = regexp.MustCompile(`\b([0-9]+)\.([0-9]+)\.([0-9]+)([-+][^\s]+)?\b`)

type SingBox struct {
	Binary     string
	Runner     platform.Runner
	Launcher   platform.Launcher
	Inspector  platform.ProcessInspector
	Authorizer platform.Authorizer
	Resolver   IPResolver
}

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func (s *SingBox) ID() dataplane.BackendID { return "sing-box" }

func (s *SingBox) Capabilities(context.Context) (dataplane.Capabilities, error) {
	return dataplane.Capabilities{
		Modes:     []dataplane.Mode{dataplane.ModeProxy, dataplane.ModeTUN},
		Protocols: []string{"vmess", "hysteria2", "anytls", "trojan"},
		Resources: map[dataplane.Mode][]dataplane.ResourceKind{
			dataplane.ModeProxy: {
				dataplane.ResourceRuntimeLock, dataplane.ResourceProcess,
				dataplane.ResourcePort, dataplane.ResourceSystemProxy,
			},
			dataplane.ModeTUN: {
				dataplane.ResourceRuntimeLock, dataplane.ResourceProcess,
				dataplane.ResourcePort, dataplane.ResourceTUN,
				dataplane.ResourceRoute, dataplane.ResourceDNS,
			},
		},
		Version: ">=1.13.14",
	}, nil
}

func (s *SingBox) Validate(ctx context.Context, request dataplane.ValidateRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(s.Binary); err != nil {
		return dataplane.NewError(
			dataplane.CodeDependency, "validate", s.ID(),
			"sing-box is not installed", err,
		)
	}
	if _, err := s.CheckVersion(); err != nil {
		return dataplane.NewError(
			dataplane.CodeDependency, "validate", s.ID(),
			err.Error(), err,
		)
	}
	port := request.Endpoint.Port
	if port <= 0 {
		port = 7890
	}
	mode := request.Mode
	if request.Purpose == dataplane.ValidationRefresh || mode == "" {
		mode = dataplane.ModeProxy
	}
	if err := s.validateNodes(request.Nodes, port, mode); err != nil {
		return dataplane.NewError(
			dataplane.CodeConfig, "validate", s.ID(),
			err.Error(), err,
		)
	}
	return ctx.Err()
}

func (s *SingBox) Render(ctx context.Context, request dataplane.RenderRequest) (dataplane.ConfigArtifact, error) {
	var (
		config map[string]any
		err    error
	)
	switch request.Mode {
	case dataplane.ModeProxy:
		config, err = BuildProxyConfig(request.Node, request.Endpoint.Port)
	case dataplane.ModeTUN:
		var dialServer, exclusion string
		dialServer, exclusion, err = s.resolveDialAddress(ctx, request.Node.Server)
		if err != nil {
			if request.Purpose == dataplane.ValidationExport {
				config, err = BuildTUNConfig(request.Node, request.Endpoint.Port)
				break
			}
			return dataplane.ConfigArtifact{}, dataplane.NewError(
				dataplane.CodeResourceConflict, "render", s.ID(),
				"Could not resolve the proxy server before configuring TUN routes", err,
			)
		}
		config, err = buildTUNConfigForDial(
			request.Node, request.Endpoint.Port, dialServer, []string{exclusion},
		)
	default:
		err = fmt.Errorf("invalid mode: %s", request.Mode)
	}
	if err != nil {
		return dataplane.ConfigArtifact{}, dataplane.NewError(
			dataplane.CodeConfig, "render", s.ID(),
			"Could not render the sing-box configuration", err,
		)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return dataplane.ConfigArtifact{}, dataplane.NewError(
			dataplane.CodeConfig, "render", s.ID(),
			"Could not render the sing-box configuration", err,
		)
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	return dataplane.ConfigArtifact{
		Backend: s.ID(), Format: "json", Filename: "sing-box.json",
		Bytes: data, SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func (s *SingBox) resolveDialAddress(ctx context.Context, server string) (string, string, error) {
	var addresses []net.IPAddr
	if ip := net.ParseIP(server); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		resolver := s.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		var err error
		addresses, err = resolver.LookupIPAddr(ctx, server)
		if err != nil {
			return "", "", err
		}
	}
	uniqueIPv4 := make(map[string]struct{}, len(addresses))
	uniqueIPv6 := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		if ipv4 := address.IP.To4(); ipv4 != nil {
			uniqueIPv4[ipv4.String()] = struct{}{}
			continue
		}
		if ipv6 := address.IP.To16(); ipv6 != nil {
			uniqueIPv6[ipv6.String()] = struct{}{}
		}
	}
	if len(uniqueIPv4) == 0 && len(uniqueIPv6) == 0 {
		return "", "", fmt.Errorf("proxy server resolved without usable IP addresses")
	}
	addressesForFamily := uniqueIPv4
	prefix := "/32"
	if len(addressesForFamily) == 0 {
		addressesForFamily = uniqueIPv6
		prefix = "/128"
	}
	ordered := make([]string, 0, len(addressesForFamily))
	for address := range addressesForFamily {
		ordered = append(ordered, address)
	}
	sort.Strings(ordered)
	return ordered[0], ordered[0] + prefix, nil
}

func (s *SingBox) Start(ctx context.Context, request dataplane.StartRequest) (dataplane.Instance, error) {
	if err := ctx.Err(); err != nil {
		return dataplane.Instance{}, err
	}
	if s.Binary == "" {
		return dataplane.Instance{}, dataplane.NewError(
			dataplane.CodeDependency, "start", s.ID(),
			"sing-box is not configured", nil,
		)
	}
	if err := os.MkdirAll(request.RuntimeDir, 0o700); err != nil {
		return dataplane.Instance{}, dataplane.NewError(
			dataplane.CodeStart, "start", s.ID(),
			"Could not prepare the sing-box runtime directory", err,
		)
	}
	log, err := os.OpenFile(
		filepath.Join(request.RuntimeDir, "sing-box.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600,
	)
	if err != nil {
		return dataplane.Instance{}, dataplane.NewError(
			dataplane.CodeStart, "start", s.ID(),
			"Could not create the sing-box log", err,
		)
	}
	defer log.Close()

	args := []string{s.Binary, "run", "-c", request.ArtifactPath, "-D", request.RuntimeDir}
	command := append([]string(nil), args...)
	environment := make(map[string]string, len(request.Environment)+2)
	for key, value := range request.Environment {
		environment[key] = value
	}
	if request.Mode == dataplane.ModeTUN {
		environment["ENABLE_DEPRECATED_LEGACY_DNS_SERVERS"] = "true"
		environment["ENABLE_DEPRECATED_OUTBOUND_DNS_RULE_ITEM"] = "true"
		if os.Geteuid() != 0 {
			authorizer := s.Authorizer
			if authorizer == nil {
				authorizer = platform.SudoAuthorizer{}
			}
			if err := authorizer.Authorize(ctx); err != nil {
				return dataplane.Instance{}, dataplane.NewError(
					dataplane.CodePermission, "start", s.ID(),
					"Could not obtain administrator authorization", err,
				)
			}
			command = append([]string{
				"sudo", "-n", "env",
				"ENABLE_DEPRECATED_LEGACY_DNS_SERVERS=true",
				"ENABLE_DEPRECATED_OUTBOUND_DNS_RULE_ITEM=true",
			}, args...)
		}
	}
	launcher := s.Launcher
	if launcher == nil {
		launcher = platform.ExecLauncher{}
	}
	handle, err := launcher.Start(ctx, platform.LaunchRequest{
		Command: command, Environment: environment,
		Stdout: log, Stderr: log,
		NewSession:      request.Mode != dataplane.ModeTUN,
		NewProcessGroup: request.Mode == dataplane.ModeTUN,
	})
	if err != nil {
		return dataplane.Instance{}, dataplane.NewError(
			dataplane.CodeStart, "start", s.ID(),
			"Could not start sing-box", err,
		)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- handle.Wait() }()

	startedAt := time.Now().UTC()
	identity := dataplane.ProcessIdentity{
		PID: handle.PID(), Executable: s.Binary,
		ArgsFingerprint: platform.FingerprintArgs(args),
		StartedAt:       startedAt,
	}
	inspector := s.Inspector
	if inspector == nil {
		inspector = platform.ExecProcessInspector{}
	}
	inspected, inspectErr := inspector.Inspect(handle.PID())
	if inspectErr == nil && inspected.PID > 0 && platform.SameProcess(inspected, identity) {
		identity = inspected
		identity.StartedAt = startedAt
	} else if request.Mode == dataplane.ModeTUN && os.Geteuid() != 0 {
		resolver, ok := inspector.(platform.DescendantProcessResolver)
		if !ok {
			cleanupErr := terminateLaunchedProcess(handle, waitResult)
			return dataplane.Instance{}, dataplane.NewError(
				dataplane.CodeStart, "start", s.ID(),
				"Could not identify the privileged sing-box process",
				errors.Join(errors.New("process inspector cannot resolve descendants"), cleanupErr),
			)
		}
		type resolveResult struct {
			identity dataplane.ProcessIdentity
			err      error
		}
		resolvedProcess := make(chan resolveResult, 1)
		go func() {
			resolved, resolveErr := resolver.ResolveDescendant(ctx, handle.PID(), identity)
			resolvedProcess <- resolveResult{identity: resolved, err: resolveErr}
		}()
		var resolved dataplane.ProcessIdentity
		select {
		case launchErr := <-waitResult:
			if launchErr == nil {
				launchErr = errors.New("launcher exited before sing-box identity was available")
			}
			return dataplane.Instance{}, dataplane.NewError(
				dataplane.CodeStart, "start", s.ID(),
				"Could not start sing-box", launchErr,
			)
		case result := <-resolvedProcess:
			resolved, err = result.identity, result.err
		}
		if err != nil {
			cleanupErr := terminateLaunchedProcess(handle, waitResult)
			return dataplane.Instance{}, dataplane.NewError(
				dataplane.CodeStart, "start", s.ID(),
				"Could not identify the privileged sing-box process",
				errors.Join(err, cleanupErr),
			)
		}
		identity = resolved
		identity.StartedAt = startedAt
	}
	return dataplane.Instance{
		ID: request.LeaseID, Backend: s.ID(), Mode: request.Mode,
		Process: identity, StartedAt: time.Now().UTC(),
		Resources: []dataplane.ResourceObservation{{
			Kind: dataplane.ResourcePort,
			Key:  net.JoinHostPort(request.Endpoint.Host, strconv.Itoa(request.Endpoint.Port)),
		}},
	}, nil
}

func terminateLaunchedProcess(
	handle platform.ProcessHandle,
	waitResult <-chan error,
) error {
	select {
	case <-waitResult:
		return nil
	default:
	}
	var cleanupErr error
	if err := handle.Terminate(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("terminate launcher: %w", err))
	} else {
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-waitResult:
			timer.Stop()
			return nil
		case <-timer.C:
			cleanupErr = errors.Join(cleanupErr, errors.New("launcher did not exit after TERM"))
		}
	}
	if err := handle.Kill(); err != nil {
		return errors.Join(cleanupErr, fmt.Errorf("kill launcher: %w", err))
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-waitResult:
		return cleanupErr
	case <-timer.C:
		return errors.Join(cleanupErr, errors.New("launcher did not exit after KILL"))
	}
}

func (s *SingBox) Stop(ctx context.Context, instance dataplane.Instance) error {
	inspector := s.Inspector
	if inspector == nil {
		inspector = platform.ExecProcessInspector{}
	}
	current, err := inspector.Inspect(instance.Process.PID)
	if err != nil {
		return nil
	}
	if !platform.SameProcess(current, instance.Process) {
		return dataplane.NewError(
			dataplane.CodeOwnershipUnknown, "stop", s.ID(),
			"sing-box process ownership could not be verified", nil,
		)
	}
	if instance.Mode == dataplane.ModeTUN && os.Geteuid() != 0 {
		authorizer := s.Authorizer
		if authorizer == nil {
			authorizer = platform.SudoAuthorizer{}
		}
		if err := authorizer.Authorize(ctx); err != nil {
			return dataplane.NewError(
				dataplane.CodePermission, "stop", s.ID(),
				"Could not obtain administrator authorization", err,
			)
		}
	}
	if err := s.signal(instance, inspector, syscall.SIGTERM); err != nil {
		return dataplane.NewError(
			dataplane.CodeStop, "stop", s.ID(),
			"Could not stop sing-box", err,
		)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := waitForProcessExit(waitCtx, inspector, instance.Process.PID); err == nil {
		return nil
	}
	if err := s.signal(instance, inspector, syscall.SIGKILL); err != nil {
		return dataplane.NewError(
			dataplane.CodeStop, "stop", s.ID(),
			"Could not stop sing-box", err,
		)
	}
	killCtx, killCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer killCancel()
	if err := waitForProcessExit(killCtx, inspector, instance.Process.PID); err != nil {
		return dataplane.NewError(
			dataplane.CodeStop, "stop", s.ID(),
			"sing-box did not exit", err,
		)
	}
	return nil
}

func (s *SingBox) signal(
	instance dataplane.Instance,
	inspector platform.ProcessInspector,
	signal syscall.Signal,
) error {
	if instance.Mode != dataplane.ModeTUN || os.Geteuid() == 0 {
		return inspector.Signal(instance.Process, signal)
	}
	name := map[syscall.Signal]string{
		syscall.SIGTERM: "TERM",
		syscall.SIGKILL: "KILL",
	}[signal]
	if name == "" {
		return fmt.Errorf("unsupported signal %d", signal)
	}
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	result, err := runner.Run([]string{
		"sudo", "-n", "kill", "-" + name, strconv.Itoa(instance.Process.PID),
	}, "", nil, 10*time.Second)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("privileged signal failed")
	}
	return nil
}

func (s *SingBox) Probe(ctx context.Context, request dataplane.ProbeRequest) (dataplane.HealthResult, error) {
	start := time.Now()
	result := dataplane.HealthResult{
		Status: dataplane.HealthUnknown, CheckedAt: start.UTC(),
	}
	if request.Kind == dataplane.ProbeOutbound && request.Node != nil {
		return s.probeOutbound(ctx, request, start)
	}
	if request.Kind != dataplane.ProbeInstance || request.Instance == nil {
		return result, dataplane.NewError(
			dataplane.CodeUnsupported, "probe", s.ID(),
			"The requested sing-box probe is not supported", nil,
		)
	}
	if s.Inspector != nil {
		current, err := s.Inspector.Inspect(request.Instance.Process.PID)
		if err != nil || !platform.SameProcess(current, request.Instance.Process) {
			result.Status, result.Reason = dataplane.HealthStopped, dataplane.CodeOwnershipUnknown
			return result, nil
		}
	}
	address := net.JoinHostPort(request.Endpoint.Host, strconv.Itoa(request.Endpoint.Port))
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	result.Latency = time.Since(start)
	if err != nil {
		result.Status, result.Reason = dataplane.HealthStarting, dataplane.CodeProbe
		return result, nil
	}
	_ = connection.Close()
	result.Status = dataplane.HealthHealthy
	result.Resources = []dataplane.ResourceObservation{{
		Kind: dataplane.ResourcePort, Key: address,
	}}
	return result, nil
}

func (s *SingBox) probeOutbound(
	ctx context.Context,
	request dataplane.ProbeRequest,
	start time.Time,
) (dataplane.HealthResult, error) {
	result := dataplane.HealthResult{
		Status: dataplane.HealthUnknown, CheckedAt: start.UTC(),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return result, dataplane.NewError(
			dataplane.CodeResourceConflict, "probe", s.ID(),
			"Could not allocate a health probe port", err,
		)
	}
	endpoint := dataplane.ListenEndpoint{
		Network: "tcp", Host: "127.0.0.1",
		Port: listener.Addr().(*net.TCPAddr).Port,
	}
	_ = listener.Close()
	root, err := os.MkdirTemp("", "fleet-health-")
	if err != nil {
		return result, dataplane.NewError(
			dataplane.CodeStart, "probe", s.ID(),
			"Could not prepare the health probe", err,
		)
	}
	defer os.RemoveAll(root)
	artifact, err := s.Render(ctx, dataplane.RenderRequest{
		Backend: s.ID(), Purpose: dataplane.ValidationProbe,
		Mode: dataplane.ModeProxy, Node: *request.Node, Endpoint: endpoint,
	})
	if err != nil {
		return result, err
	}
	path := filepath.Join(root, artifact.Filename)
	if err := os.WriteFile(path, artifact.Bytes, 0o600); err != nil {
		return result, dataplane.NewError(
			dataplane.CodeConfig, "probe", s.ID(),
			"Could not write the health probe configuration", err,
		)
	}
	instance, err := s.Start(ctx, dataplane.StartRequest{
		ArtifactPath: path, RuntimeDir: root, Mode: dataplane.ModeProxy,
		LeaseID: "health-probe", Endpoint: endpoint,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = s.Stop(cleanupCtx, instance)
	}()
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		probe, probeErr := s.Probe(ctx, dataplane.ProbeRequest{
			Kind: dataplane.ProbeInstance, Instance: &instance, Endpoint: endpoint,
		})
		if probeErr != nil {
			return result, probeErr
		}
		if probe.Status == dataplane.HealthHealthy {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !ready {
		result.Status, result.Reason = dataplane.HealthUnhealthy, dataplane.CodeStart
		return result, nil
	}
	target := request.Target
	if target == "" {
		target = os.Getenv("FLEET_HEALTH_URL")
	}
	if target == "" {
		target = "https://api.github.com"
	}
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	curlStart := time.Now()
	curl, runErr := runner.Run([]string{
		"curl", "--proxy", fmt.Sprintf("http://127.0.0.1:%d", endpoint.Port),
		"--noproxy", "", "--silent", "--output", "/dev/null",
		"--write-out", "%{http_code}", "--connect-timeout", "10",
		"--max-time", "10", target,
	}, "", nil, 12*time.Second)
	result.Latency = time.Since(curlStart)
	if runErr == nil && curl.Code == 0 && validHTTPStatus(curl.Stdout) {
		result.Status = dataplane.HealthHealthy
		return result, nil
	}
	result.Status, result.Reason = dataplane.HealthUnhealthy, dataplane.CodeProbe
	return result, nil
}

func validHTTPStatus(value string) bool {
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, "")
	return len(value) == 3 && value[0] >= '1' && value[0] <= '5'
}

func waitForProcessExit(ctx context.Context, inspector platform.ProcessInspector, pid int) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := inspector.Inspect(pid); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s SingBox) CheckVersion() ([3]int, error) {
	var zero [3]int
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	result, err := runner.Run([]string{s.Binary, "version"}, "", nil, 10*time.Second)
	match := versionRE.FindStringSubmatch(result.Stdout)
	message := "sing-box >= 1.13.14 is required; upgrade with: brew upgrade sing-box"
	if err != nil || result.Code != 0 || match == nil {
		return zero, model.NewError("sing-box", message, err)
	}
	version := [3]int{}
	for i := range 3 {
		version[i], _ = strconv.Atoi(match[i+1])
	}
	if match[4] != "" && match[4][0] == '-' || version[0] < 1 ||
		(version[0] == 1 && version[1] < 13) ||
		(version[0] == 1 && version[1] == 13 && version[2] < 14) {
		return zero, model.NewError("sing-box", fmt.Sprintf("%s (detected %d.%d.%d)", message, version[0], version[1], version[2]), nil)
	}
	return version, nil
}

func (s SingBox) ValidateNodes(nodes []model.Node, port int) error {
	return s.validateNodes(nodes, port, dataplane.ModeProxy)
}

func (s SingBox) validateNodes(nodes []model.Node, port int, mode dataplane.Mode) error {
	if _, err := os.Stat(s.Binary); err != nil {
		return model.NewError("sing-box", "sing-box is not installed", err)
	}
	if _, err := s.CheckVersion(); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "fleet-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	for i, node := range nodes {
		var config map[string]any
		switch mode {
		case dataplane.ModeProxy:
			config, err = BuildProxyConfig(node, port)
		case dataplane.ModeTUN:
			config, err = BuildTUNConfig(node, port)
		default:
			err = fmt.Errorf("invalid mode: %s", mode)
		}
		if err != nil {
			return err
		}
		data, _ := json.Marshal(config)
		path := filepath.Join(root, fmt.Sprintf("node-%d.json", i))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		result, runErr := runner.Run([]string{s.Binary, "check", "-c", path}, "", nil, 30*time.Second)
		if runErr != nil || result.Code != 0 {
			return model.NewError("sing-box", fmt.Sprintf("Node '%s' failed sing-box validation", node.Name), runErr)
		}
	}
	return nil
}
