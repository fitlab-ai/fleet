package platform

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

type ResourceSnapshot map[dataplane.ResourceKind][]string

type FullTunnelObservation struct {
	Interface string
	Family    string
	Shape     string
}

// This is deliberately an internal product heuristic rather than user
// configuration: exact default routes still win, while broad TUN route sets
// need one stable threshold across every Fleet invocation.
const fullTunnelPublicIPv4CoveragePercent uint64 = 95

type ipv4Interval struct {
	start uint32
	end   uint32
}

type ResourceInspector interface {
	PortOwner(context.Context, dataplane.ListenEndpoint) (dataplane.ProcessIdentity, error)
	Snapshot(context.Context, []dataplane.ResourceKind) (ResourceSnapshot, error)
	FullTunnels(context.Context) ([]FullTunnelObservation, error)
}

type PortBinder interface {
	Listen(context.Context, string, string) (io.Closer, error)
	ListenPacket(context.Context, string, string) (io.Closer, error)
}

type NetPortBinder struct {
	ListenConfig net.ListenConfig
}

func (b NetPortBinder) Listen(ctx context.Context, network, address string) (io.Closer, error) {
	return b.ListenConfig.Listen(ctx, network, address)
}

func (b NetPortBinder) ListenPacket(ctx context.Context, network, address string) (io.Closer, error) {
	return b.ListenConfig.ListenPacket(ctx, network, address)
}

// LegacyResourceSnapshotter reproduces the opaque snapshot format persisted by
// schema-v2 releases before resource ownership was recorded separately.
type LegacyResourceSnapshotter interface {
	SnapshotLegacy(context.Context, []dataplane.ResourceKind) (ResourceSnapshot, error)
}

type ExecResourceInspector struct{}

func (ExecResourceInspector) PortOwner(ctx context.Context, endpoint dataplane.ListenEndpoint) (dataplane.ProcessIdentity, error) {
	command := portOwnerCommand(endpoint)
	output, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return dataplane.ProcessIdentity{}, os.ErrNotExist
		}
		return dataplane.ProcessIdentity{}, err
	}
	return ParseLsofOwner(string(output))
}

func portOwnerCommand(endpoint dataplane.ListenEndpoint) []string {
	if strings.EqualFold(endpoint.Network, "udp") {
		return []string{"lsof", "-nP", fmt.Sprintf("-iUDP:%d", endpoint.Port), "-Fpca"}
	}
	return []string{
		"lsof", "-nP", fmt.Sprintf("-iTCP:%d", endpoint.Port), "-sTCP:LISTEN", "-Fpca",
	}
}

func ParseLsofOwner(output string) (dataplane.ProcessIdentity, error) {
	var pid int
	var command string
	var args []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			command = line[1:]
		case 'a':
			args = append(args, line[1:])
		}
	}
	if pid <= 0 {
		return dataplane.ProcessIdentity{}, fmt.Errorf("port owner unavailable")
	}
	if len(args) == 0 {
		args = []string{command}
	}
	return dataplane.ProcessIdentity{
		PID: pid, Executable: command, ArgsFingerprint: FingerprintArgs(args),
	}, nil
}

func (ExecResourceInspector) Snapshot(ctx context.Context, kinds []dataplane.ResourceKind) (ResourceSnapshot, error) {
	return snapshotResources(ctx, kinds, false)
}

func (ExecResourceInspector) FullTunnels(ctx context.Context) ([]FullTunnelObservation, error) {
	ipv4, err := exec.CommandContext(ctx, "netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect IPv4 routes: %w", err)
	}
	ipv6, err := exec.CommandContext(ctx, "netstat", "-rn", "-f", "inet6").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect IPv6 routes: %w", err)
	}
	if len(strings.TrimSpace(string(ipv4))) == 0 || len(strings.TrimSpace(string(ipv6))) == 0 {
		return nil, fmt.Errorf("inspect routes: netstat returned an empty route table")
	}
	return ParseFullTunnelObservations(string(ipv4), string(ipv6))
}

func (ExecResourceInspector) SnapshotLegacy(
	ctx context.Context,
	kinds []dataplane.ResourceKind,
) (ResourceSnapshot, error) {
	return snapshotResources(ctx, kinds, true)
}

func snapshotResources(
	ctx context.Context,
	kinds []dataplane.ResourceKind,
	legacy bool,
) (ResourceSnapshot, error) {
	snapshot := ResourceSnapshot{}
	for _, kind := range kinds {
		var commands [][]string
		switch kind {
		case dataplane.ResourceTUN:
			commands = [][]string{{"ifconfig", "-l"}}
		case dataplane.ResourceRoute:
			commands = [][]string{{"netstat", "-rn", "-f", "inet"}}
			if !legacy {
				commands = append(commands, []string{"netstat", "-rn", "-f", "inet6"})
			}
		case dataplane.ResourceDNS:
			commands = [][]string{{"scutil", "--dns"}}
		default:
			continue
		}
		var output strings.Builder
		for _, command := range commands {
			part, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
			if err != nil {
				return nil, err
			}
			output.Write(part)
			output.WriteByte('\n')
		}
		snapshot[kind] = fingerprintResourceOutput(kind, output.String(), legacy)
	}
	return snapshot, nil
}

func fingerprintResourceOutput(
	kind dataplane.ResourceKind,
	output string,
	legacy bool,
) []string {
	if legacy {
		return FingerprintLines(output)
	}
	switch kind {
	case dataplane.ResourceRoute:
		return FingerprintRouteLines(output)
	case dataplane.ResourceTUN:
		return FingerprintInterfaceNames(output)
	default:
		return FingerprintLines(output)
	}
}

func FingerprintRouteLines(output string) []string {
	var stable []string
	for _, line := range strings.Split(output, "\n") {
		_, interfaceName, _, normalized, ok := parseRouteLine(line)
		if !ok {
			continue
		}
		stable = append(stable,
			fingerprint(interfaceName)+":"+fingerprint(normalized),
		)
	}
	sort.Strings(stable)
	return stable
}

func ParseFullTunnelObservations(ipv4, ipv6 string) ([]FullTunnelObservation, error) {
	var observations []FullTunnelObservation
	for _, input := range []struct {
		family string
		output string
	}{
		{family: "ipv4", output: ipv4},
		{family: "ipv6", output: ipv6},
	} {
		familyObservations, err := parseFullTunnelsForFamily(input.family, input.output)
		if err != nil {
			return nil, err
		}
		observations = append(observations, familyObservations...)
	}
	return observations, nil
}

func parseFullTunnelsForFamily(family, output string) ([]FullTunnelObservation, error) {
	if strings.TrimSpace(output) != "" && !hasRouteTableHeader(output) {
		return nil, fmt.Errorf("%s route table format is not recognized", family)
	}
	destinations := map[string]map[string]struct{}{}
	for _, line := range strings.Split(output, "\n") {
		destination, interfaceName, flags, _, ok := parseRouteLine(line)
		if !ok || !strings.HasPrefix(interfaceName, "utun") || strings.Contains(flags, "I") {
			continue
		}
		if destinations[interfaceName] == nil {
			destinations[interfaceName] = map[string]struct{}{}
		}
		destinations[interfaceName][normalizeRouteDestination(family, destination)] = struct{}{}
	}

	interfaces := make([]string, 0, len(destinations))
	for interfaceName := range destinations {
		interfaces = append(interfaces, interfaceName)
	}
	sort.Strings(interfaces)

	low, high := splitRouteDestinations(family)
	var observations []FullTunnelObservation
	var lowSeen, highSeen, pairedSeen bool
	for _, interfaceName := range interfaces {
		routes := destinations[interfaceName]
		_, hasDefault := routes["default"]
		_, hasLow := routes[low]
		_, hasHigh := routes[high]
		lowSeen = lowSeen || hasLow
		highSeen = highSeen || hasHigh
		shape := ""
		switch {
		case hasDefault:
			shape = "default"
		case hasLow && hasHigh:
			shape = "split"
			pairedSeen = true
		case family == "ipv4" && coversPublicIPv4(routes):
			shape = "coverage"
		}
		if shape != "" {
			observations = append(observations, FullTunnelObservation{
				Interface: interfaceName, Family: family, Shape: shape,
			})
		}
	}
	if lowSeen && highSeen && !pairedSeen {
		return nil, fmt.Errorf("%s split default routes span multiple tunnel interfaces", family)
	}
	return observations, nil
}

func hasRouteTableHeader(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "Destination" &&
			fields[1] == "Gateway" && fields[2] == "Flags" && fields[3] == "Netif" {
			return true
		}
	}
	return false
}

func parseRouteLine(line string) (
	destination, interfaceName, flags, normalized string,
	ok bool,
) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] == "Destination" {
		return "", "", "", "", false
	}
	if _, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
		return "", "", "", "", false
	}
	interfaceName = fields[len(fields)-1]
	if interfaceName == "!" {
		if len(fields) < 5 {
			return "", "", "", "", false
		}
		interfaceName = fields[len(fields)-2]
	}
	return fields[0], interfaceName, fields[2], strings.Join(fields, " "), true
}

func normalizeRouteDestination(family, destination string) string {
	destination = strings.ToLower(destination)
	if destination == "default" || destination == "0.0.0.0/0" || destination == "::/0" {
		return "default"
	}
	if family == "ipv4" {
		switch destination {
		case "0.0.0.0/1":
			return "0/1"
		case "128.0/1", "128.0.0.0/1":
			return "128/1"
		}
	}
	return destination
}

func splitRouteDestinations(family string) (string, string) {
	if family == "ipv6" {
		return "::/1", "8000::/1"
	}
	return "0/1", "128/1"
}

func coversPublicIPv4(routes map[string]struct{}) bool {
	intervals := make([]ipv4Interval, 0, len(routes))
	for destination := range routes {
		interval, ok := parseIPv4RouteInterval(destination)
		if ok {
			intervals = append(intervals, interval)
		}
	}
	intervals = mergeIPv4Intervals(intervals)
	if len(intervals) == 0 {
		return false
	}

	public := publicIPv4Intervals()
	total := coveredIPv4Count(public, public)
	covered := coveredIPv4Count(intervals, public)
	low := []ipv4Interval{{start: 0, end: 1<<31 - 1}}
	high := []ipv4Interval{{start: 1 << 31, end: ^uint32(0)}}
	return coveredIPv4Count(intervals, intersectIPv4Intervals(public, low)) > 0 &&
		coveredIPv4Count(intervals, intersectIPv4Intervals(public, high)) > 0 &&
		covered*100 >= total*fullTunnelPublicIPv4CoveragePercent
}

func parseIPv4RouteInterval(destination string) (ipv4Interval, bool) {
	destination = strings.TrimSpace(strings.ToLower(destination))
	if destination == "default" || destination == "0.0.0.0/0" {
		return ipv4Interval{start: 0, end: ^uint32(0)}, true
	}

	address, prefixText, hasPrefix := strings.Cut(destination, "/")
	parts := strings.Split(address, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return ipv4Interval{}, false
	}
	var value uint32
	for _, part := range parts {
		octet, err := strconv.Atoi(part)
		if err != nil || octet < 0 || octet > 255 {
			return ipv4Interval{}, false
		}
		value = value<<8 | uint32(octet)
	}
	value <<= 8 * (4 - len(parts))

	// Darwin netstat abbreviates trailing zero octets, so "1" is 1.0.0.0/8
	// and "128.0/1" is 128.0.0.0/1.
	prefix := len(parts) * 8
	if hasPrefix {
		parsed, err := strconv.Atoi(prefixText)
		if err != nil || parsed < 0 || parsed > 32 {
			return ipv4Interval{}, false
		}
		prefix = parsed
	}
	if prefix == 0 {
		return ipv4Interval{start: 0, end: ^uint32(0)}, true
	}
	mask := ^uint32(0) << (32 - prefix)
	start := value & mask
	return ipv4Interval{start: start, end: start | ^mask}, true
}

func mergeIPv4Intervals(intervals []ipv4Interval) []ipv4Interval {
	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start == intervals[j].start {
			return intervals[i].end < intervals[j].end
		}
		return intervals[i].start < intervals[j].start
	})
	merged := make([]ipv4Interval, 0, len(intervals))
	current := intervals[0]
	for _, next := range intervals[1:] {
		if uint64(next.start) <= uint64(current.end)+1 {
			if next.end > current.end {
				current.end = next.end
			}
			continue
		}
		merged = append(merged, current)
		current = next
	}
	return append(merged, current)
}

func publicIPv4Intervals() []ipv4Interval {
	public := []ipv4Interval{{start: 0, end: ^uint32(0)}}
	// Exclude private, shared, loopback, link-local, documentation,
	// benchmarking, multicast, and reserved ranges from the denominator.
	for _, destination := range []string{
		"0/8", "10/8", "100.64/10", "127/8", "169.254/16", "172.16/12",
		"192.0.0/24", "192.0.2/24", "192.88.99/24", "192.168/16",
		"198.18/15", "198.51.100/24", "203.0.113/24", "224/4", "240/4",
	} {
		excluded, ok := parseIPv4RouteInterval(destination)
		if !ok {
			continue
		}
		var remaining []ipv4Interval
		for _, interval := range public {
			switch {
			case excluded.end < interval.start || excluded.start > interval.end:
				remaining = append(remaining, interval)
			default:
				if interval.start < excluded.start {
					remaining = append(remaining, ipv4Interval{start: interval.start, end: excluded.start - 1})
				}
				if excluded.end < interval.end {
					remaining = append(remaining, ipv4Interval{start: excluded.end + 1, end: interval.end})
				}
			}
		}
		public = remaining
	}
	return public
}

func intersectIPv4Intervals(left, right []ipv4Interval) []ipv4Interval {
	var intersections []ipv4Interval
	for _, first := range left {
		for _, second := range right {
			start := max(first.start, second.start)
			end := min(first.end, second.end)
			if start <= end {
				intersections = append(intersections, ipv4Interval{start: start, end: end})
			}
		}
	}
	return intersections
}

func coveredIPv4Count(routes, universe []ipv4Interval) uint64 {
	var covered uint64
	for _, intersection := range intersectIPv4Intervals(routes, universe) {
		covered += uint64(intersection.end) - uint64(intersection.start) + 1
	}
	return covered
}

func FingerprintInterfaceNames(output string) []string {
	fields := strings.Fields(output)
	fingerprints := make([]string, 0, len(fields))
	for _, field := range fields {
		fingerprints = append(fingerprints, fingerprint(field))
	}
	sort.Strings(fingerprints)
	return fingerprints
}

func FingerprintLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, fingerprint(line))
		}
	}
	sort.Strings(lines)
	return lines
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
